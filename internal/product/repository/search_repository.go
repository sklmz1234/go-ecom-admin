package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/elastic/go-elasticsearch/v8"
	"go.uber.org/zap"

	"go-ecom-admin/internal/product/model"
)

// —— 阶段 5A：Elasticsearch 商品搜索 ——
//
// 架构（搜索引擎的经典用法，面试可展开）：
//   ES 只做「召回」——索引里只放低变字段（name/description/image_url），
//   查询也只取回商品 id 列表（_source: false），按 _score 相关度排序；
//   MySQL 做「回表」——用 id 批量查出实时完整数据（stock/price 是高变字段，
//   下单扣减走 DeductStock 不会同步 ES，进索引必然过期）。
//
// 和缓存层同一个哲学：搜索引擎是加速层不是正确性依赖。
// ES 挂了 → searchRepository 装饰器降级到 gorm 的 LIKE；
// 服务启动时 ES 就连不上 → main 里干脆不包这层装饰器，裸 gorm 直接 LIKE。

// ProductSearcher 抽象搜索引擎的最小能力集。装饰器面向这个接口编程，
// 单测可以用假实现注入（不用起真 ES），和 Repository 接口的用意一致。
type ProductSearcher interface {
	// Search 按关键词召回商品 id，按相关度（_score）降序，附带命中总数。
	Search(ctx context.Context, keyword string, page, pageSize int) (ids []uint64, total int64, err error)
	// Index 写入/覆盖一个商品文档（doc _id = 商品 id，天然幂等）。
	Index(ctx context.Context, p *model.Product) error
	// Delete 删除商品文档。商品已被删除时再删是 no-op，不算错误。
	Delete(ctx context.Context, id uint64) error
}

// ESSearcher 是 ProductSearcher 的 Elasticsearch 实现。
type ESSearcher struct {
	client *elasticsearch.Client
	index  string
	log    *zap.Logger
}

// productDoc 是 ES 文档结构。刻意没有 stock/price：高变字段进索引必然
// 过期（下单扣库存不会双写 ES），展示用的实时数据一律回表拿。
type productDoc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ImageURL    string `json:"image_url"`
}

// indexMapping 建索引时的 mapping：
//   - name/description 用 IK 分词（ik_max_word 索引时最细粒度切词，
//     ik_smart 查询时粗粒度）——standard 分词器把中文按单字切，相关度
//     排序会失效，这是选 IK 的直接原因（镜像 Dockerfile 里有完整注释）；
//   - image_url 是 keyword 且 index:false——只存储不参与搜索，省索引体积；
//     其实 _source:false 的查询用不到它，写入它是为了以后想"只读 ES 不回表"
//     的优化留余地（届时改动只在查询侧）。
const indexMapping = `{
  "mappings": {
    "properties": {
      "name":        {"type": "text", "analyzer": "ik_max_word", "search_analyzer": "ik_smart"},
      "description": {"type": "text", "analyzer": "ik_max_word", "search_analyzer": "ik_smart"},
      "image_url":   {"type": "keyword", "index": false}
    }
  }
}`

// NewESSearcher 连接 ES 并确保索引存在（不存在则按 IK mapping 创建）。
// Ping 失败直接返回错误——调用方（main）据此决定不启用搜索装饰器，
// 整个服务降级为 LIKE，理由见文件头注释。
func NewESSearcher(addr, index string, log *zap.Logger) (*ESSearcher, error) {
	client, err := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{addr}})
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: new client: %w", err)
	}
	s := &ESSearcher{client: client, index: index, log: log}

	res, err := client.Ping()
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: ping: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch: ping: status %s", res.Status())
	}

	if err := s.ensureIndex(context.Background()); err != nil {
		return nil, fmt.Errorf("elasticsearch: ensure index: %w", err)
	}
	return s, nil
}

// ensureIndex 索引不存在才创建。已存在时完全不动——mapping 变更（比如
// 以后加字段）需要显式重建，启动时静默改 mapping 是事故温床。
func (s *ESSearcher) ensureIndex(ctx context.Context) error {
	res, err := s.client.Indices.Exists([]string{s.index}, s.client.Indices.Exists.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 200 {
		return nil
	}

	createRes, err := s.client.Indices.Create(s.index,
		s.client.Indices.Create.WithContext(ctx),
		s.client.Indices.Create.WithBody(strings.NewReader(indexMapping)),
	)
	if err != nil {
		return err
	}
	defer createRes.Body.Close()
	if createRes.IsError() {
		body, _ := io.ReadAll(createRes.Body)
		return fmt.Errorf("create index %q: %s: %s", s.index, createRes.Status(), strings.TrimSpace(string(body)))
	}
	s.log.Info("elasticsearch index created", zap.String("index", s.index))
	return nil
}

// Search 用 multi_match 在 name（权重 ×2）和 description 上查相关度。
// name 加权是因为商品名命中比描述命中更能代表用户意图（搜"耳机"，
// 名字叫耳机的应该排在描述里提了一嘴耳机的前面）。
//
// _source:false 是"召回+回表"架构的关键：查询只取 _id 和 _score，
// 不回传文档内容——网络开销最小，也从代码上固化了"详情必须回表拿实时的"
// 这个纪律。
func (s *ESSearcher) Search(ctx context.Context, keyword string, page, pageSize int) ([]uint64, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	query := map[string]any{
		"_source": false,
		"query": map[string]any{
			"multi_match": map[string]any{
				"query":  keyword,
				"fields": []string{"name^2", "description"},
			},
		},
		"from": (page - 1) * pageSize,
		"size": pageSize,
		// 7.x 起 total 默认只统计到 10000 上限，分页 total 展示要精确值。
		"track_total_hits": true,
	}
	body, err := json.Marshal(query)
	if err != nil {
		return nil, 0, fmt.Errorf("elasticsearch: marshal query: %w", err)
	}

	res, err := s.client.Search(
		s.client.Search.WithContext(ctx),
		s.client.Search.WithIndex(s.index),
		s.client.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, 0, fmt.Errorf("elasticsearch: search: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		respBody, _ := io.ReadAll(res.Body)
		return nil, 0, fmt.Errorf("elasticsearch: search: %s: %s", res.Status(), strings.TrimSpace(string(respBody)))
	}

	// 只解析用得上的字段，不做全量文档反序列化（_source 本来就是关的）。
	var parsed struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID string `json:"_id"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, 0, fmt.Errorf("elasticsearch: decode response: %w", err)
	}

	ids := make([]uint64, 0, len(parsed.Hits.Hits))
	for _, hit := range parsed.Hits.Hits {
		id, err := strconv.ParseUint(hit.ID, 10, 64)
		if err != nil {
			// doc _id 全部来自本服务的 Index（商品 id），解析失败说明
			// 索引里混入了异物——跳过并留日志，不让一条脏数据搞挂整个搜索。
			s.log.Warn("elasticsearch hit has non-numeric id, skipped",
				zap.String("hit_id", hit.ID), zap.String("index", s.index))
			continue
		}
		ids = append(ids, id)
	}
	return ids, parsed.Hits.Total.Value, nil
}

// Index 写入/覆盖商品文档。doc _id 用商品 id：同一商品重复写是覆盖
// 而不是新增，天然幂等——双写重试不会产生重复文档。
// Refresh:true 让写入立即可搜（默认 1s 刷新间隔内搜不到），
// 学习项目数据量小，用写延迟换"管理台改完 C 端立刻搜到"的体验。
func (s *ESSearcher) Index(ctx context.Context, p *model.Product) error {
	body, err := json.Marshal(productDoc{
		Name:        p.Name,
		Description: p.Description,
		ImageURL:    p.ImageURL,
	})
	if err != nil {
		return fmt.Errorf("elasticsearch: marshal doc: %w", err)
	}

	res, err := s.client.Index(s.index, bytes.NewReader(body),
		s.client.Index.WithContext(ctx),
		s.client.Index.WithDocumentID(strconv.FormatUint(p.ID, 10)),
		s.client.Index.WithRefresh("true"),
	)
	if err != nil {
		return fmt.Errorf("elasticsearch: index doc: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		respBody, _ := io.ReadAll(res.Body)
		return fmt.Errorf("elasticsearch: index doc %d: %s: %s", p.ID, res.Status(), strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (s *ESSearcher) Delete(ctx context.Context, id uint64) error {
	res, err := s.client.Delete(s.index, strconv.FormatUint(id, 10),
		s.client.Delete.WithContext(ctx),
		s.client.Delete.WithRefresh("true"),
	)
	if err != nil {
		return fmt.Errorf("elasticsearch: delete doc: %w", err)
	}
	defer res.Body.Close()
	// 404 = 文档本来就不存在（比如商品创建于 ES 上线之前）——
	// 删除的语义是"确保不存在"，已经不存在就是目标状态，不算错误。
	if res.IsError() && res.StatusCode != 404 {
		respBody, _ := io.ReadAll(res.Body)
		return fmt.Errorf("elasticsearch: delete doc %d: %s: %s", id, res.Status(), strings.TrimSpace(string(respBody)))
	}
	return nil
}

// —— searchRepository：ES 召回 + MySQL 回表的装饰器 ——

// searchRepository 和 cachedRepository 是同一个模式的第二次应用：
// 实现同一个 Repository 接口，包住下一层（gorm 实现），在
// SearchByKeyword 一个方法上做增强（ES 召回），其余方法全部透传。
type searchRepository struct {
	next     Repository
	searcher ProductSearcher
	log      *zap.Logger
}

// NewSearchRepository 用搜索引擎包装一个已有 Repository，返回的仍是
// Repository 接口。service 层感知不到搜索实现的存在。
func NewSearchRepository(next Repository, searcher ProductSearcher, log *zap.Logger) Repository {
	return &searchRepository{next: next, searcher: searcher, log: log}
}

// SearchByKeyword 三步走：ES 召回 id（按 _score 有序）→ MySQL 回表
// 拿实时完整数据 → 按召回顺序重排。
//
// 为什么必须重排：WHERE id IN (...) 不保证返回顺序（MySQL 按主键/
// 索引顺序返回），不重排的话相关度排序在回表这一步就丢了——
// "召回+回表"架构里最容易漏的细节。
//
// ES 故障降级：召回失败时退回下一层的 LIKE 实现并记 WARN。
// 搜索是浏览路径不是交易路径，"搜得粗糙"好过"搜不了"——
// 和 cachedRepository "Redis 故障降级回源"（cached_repository.go:77）
// 是同一个取舍。
func (r *searchRepository) SearchByKeyword(ctx context.Context, keyword string, page, pageSize int) ([]*model.Product, int64, error) {
	ids, total, err := r.searcher.Search(ctx, keyword, page, pageSize)
	if err != nil {
		r.log.Warn("elasticsearch search failed, falling back to MySQL LIKE",
			zap.String("keyword", keyword), zap.Error(err))
		return r.next.SearchByKeyword(ctx, keyword, page, pageSize)
	}
	if len(ids) == 0 {
		return []*model.Product{}, total, nil
	}

	products, err := r.next.ListByIDs(ctx, ids)
	if err != nil {
		return nil, 0, err
	}

	// 按 ES 召回顺序重排：建 id → product 索引，按 ids 顺序重取。
	byID := make(map[uint64]*model.Product, len(products))
	for _, p := range products {
		byID[p.ID] = p
	}
	ordered := make([]*model.Product, 0, len(ids))
	for _, id := range ids {
		if p, ok := byID[id]; ok {
			ordered = append(ordered, p)
		}
		// id 在 ES 有但 MySQL 没有 = 双写不一致（商品删了但 ES 文档没删掉）。
		// 跳过即可，降级 LIKE 和后续 reindex 会收敛这种脏数据。
	}
	return ordered, total, nil
}

// 其余方法全部透传——装饰器只增强搜索一个方法，这是它和缓存装饰器
// 共享的纪律：增强点显式可见，不存在"悄悄改变行为"的方法。

func (r *searchRepository) Create(ctx context.Context, p *model.Product) error {
	return r.next.Create(ctx, p)
}

func (r *searchRepository) GetByID(ctx context.Context, id uint64) (*model.Product, error) {
	return r.next.GetByID(ctx, id)
}

func (r *searchRepository) Update(ctx context.Context, p *model.Product) error {
	return r.next.Update(ctx, p)
}

func (r *searchRepository) Delete(ctx context.Context, id uint64) error {
	return r.next.Delete(ctx, id)
}

func (r *searchRepository) List(ctx context.Context, page, pageSize int) ([]*model.Product, int64, error) {
	return r.next.List(ctx, page, pageSize)
}

func (r *searchRepository) ListByIDs(ctx context.Context, ids []uint64) ([]*model.Product, error) {
	return r.next.ListByIDs(ctx, ids)
}

func (r *searchRepository) DeductStock(ctx context.Context, productID uint64, quantity int32) (*model.Product, error) {
	return r.next.DeductStock(ctx, productID, quantity)
}

func (r *searchRepository) RestoreStock(ctx context.Context, productID uint64, quantity int32) (*model.Product, error) {
	return r.next.RestoreStock(ctx, productID, quantity)
}

func (r *searchRepository) RestoreStockIdempotent(ctx context.Context, messageID string, productID uint64, quantity int32) (*model.Product, error) {
	return r.next.RestoreStockIdempotent(ctx, messageID, productID, quantity)
}

// 编译期断言：装饰器必须始终实现完整 Repository 接口，
// 接口加方法时漏了透传会在编译期暴露，而不是线上才发现。
var _ Repository = (*searchRepository)(nil)
