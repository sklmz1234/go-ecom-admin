// 搜索链路（阶段 5A）单元测试，分两层：
//
//   - searchRepository 装饰器：用假 ProductSearcher + MockRepository 验证
//     "ES 召回 → 回表 → 按 _score 重排" 和 "ES 故障降级 LIKE" 两条路径，
//     不需要起真 ES（装饰器面向 ProductSearcher 接口编程的回报）；
//   - gormRepository 的 LIKE 兜底实现：sqlite :memory: 跑真实 SQL，
//     重点考点是 LIKE 通配符转义（用户输入的 % 不能变成全表匹配）。
package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"go-ecom-admin/internal/product/model"
	"go-ecom-admin/internal/product/repository/mocks"
)

// fakeSearcher 是 ProductSearcher 的测试替身：Search 返回预置结果，
// Index/Delete 录制调用（双写测试的断言对象），也可注入错误模拟 ES 故障。
type fakeSearcher struct {
	ids   []uint64
	total int64
	err   error

	indexed   []*model.Product
	deleted   []uint64
	indexErr  error
	deleteErr error
}

func (f *fakeSearcher) Search(_ context.Context, _ string, _, _ int) ([]uint64, int64, error) {
	return f.ids, f.total, f.err
}

func (f *fakeSearcher) Index(_ context.Context, p *model.Product) error {
	f.indexed = append(f.indexed, p)
	return f.indexErr
}

func (f *fakeSearcher) Delete(_ context.Context, id uint64) error {
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

// —— 同步双写（决策点 B）测试 ——

// TestCreate_DualWritesES：MySQL 写入成功后必须同步索引同一商品到 ES，
// 这是"管理台建商品、C 端立刻能搜到"（验收第 7 条）的机制保证。
func TestCreate_DualWritesES(t *testing.T) {
	next := mocks.NewMockRepository(t)
	searcher := &fakeSearcher{}
	p := &model.Product{Name: "蓝牙耳机", Description: "主动降噪", ImageURL: "https://x/y.jpg"}
	next.EXPECT().Create(mock.Anything, p).Return(nil)

	repo := NewSearchRepository(next, searcher, zaptest.NewLogger(t))
	require.NoError(t, repo.Create(context.Background(), p))

	require.Len(t, searcher.indexed, 1)
	assert.Same(t, p, searcher.indexed[0])
}

// TestCreate_ESFailureDoesNotBlock：双写失败只能记 WARN，主流程照常成功——
// ES 是加速层不是正确性依赖，它的可用性不能绑进交易路径。
func TestCreate_ESFailureDoesNotBlock(t *testing.T) {
	next := mocks.NewMockRepository(t)
	searcher := &fakeSearcher{indexErr: errors.New("es: connection refused")}
	p := &model.Product{Name: "蓝牙耳机"}
	next.EXPECT().Create(mock.Anything, p).Return(nil)

	repo := NewSearchRepository(next, searcher, zaptest.NewLogger(t))
	require.NoError(t, repo.Create(context.Background(), p), "ES 双写失败不能让 Create 失败")
	assert.Len(t, searcher.indexed, 1, "索引动作确实尝试过")
}

// TestCreate_MySQLFailureSkipsES：MySQL 没写成就不能写 ES，
// 否则索引里会出现库里不存在的幽灵商品（搜得到、点详情 404）。
func TestCreate_MySQLFailureSkipsES(t *testing.T) {
	next := mocks.NewMockRepository(t)
	searcher := &fakeSearcher{}
	p := &model.Product{Name: "蓝牙耳机"}
	next.EXPECT().Create(mock.Anything, p).Return(errors.New("db: deadlock"))

	repo := NewSearchRepository(next, searcher, zaptest.NewLogger(t))
	require.Error(t, repo.Create(context.Background(), p))
	assert.Empty(t, searcher.indexed)
}

// TestUpdate_DualWritesES：service 的 Update 是整体替换语义，传给 repo 的
// model 带着最新 name/description/image_url，装饰器应原样索引它。
func TestUpdate_DualWritesES(t *testing.T) {
	next := mocks.NewMockRepository(t)
	searcher := &fakeSearcher{}
	p := &model.Product{ID: 7, Name: "新名字耳机", Description: "改名后"}
	next.EXPECT().Update(mock.Anything, p).Return(nil)

	repo := NewSearchRepository(next, searcher, zaptest.NewLogger(t))
	require.NoError(t, repo.Update(context.Background(), p))

	require.Len(t, searcher.indexed, 1)
	assert.Equal(t, "新名字耳机", searcher.indexed[0].Name)
}

// TestUpdate_ESFailureDoesNotBlock：同 Create——改名后 ES 写失败，
// 顶多是搜索里暂时还是旧名字（降级 LIKE 兜底），不能算更新失败。
func TestUpdate_ESFailureDoesNotBlock(t *testing.T) {
	next := mocks.NewMockRepository(t)
	searcher := &fakeSearcher{indexErr: errors.New("es: circuit open")}
	p := &model.Product{ID: 7, Name: "新名字耳机"}
	next.EXPECT().Update(mock.Anything, p).Return(nil)

	repo := NewSearchRepository(next, searcher, zaptest.NewLogger(t))
	require.NoError(t, repo.Update(context.Background(), p))
}

// TestDelete_DualWritesES：删除商品必须同步删 ES 文档，
// 否则会搜到已删除的商品（回表时被跳过，但 total 虚高）。
func TestDelete_DualWritesES(t *testing.T) {
	next := mocks.NewMockRepository(t)
	searcher := &fakeSearcher{}
	next.EXPECT().Delete(mock.Anything, uint64(7)).Return(nil)

	repo := NewSearchRepository(next, searcher, zaptest.NewLogger(t))
	require.NoError(t, repo.Delete(context.Background(), 7))

	assert.Equal(t, []uint64{7}, searcher.deleted)
}

// TestDelete_ESFailureDoesNotBlock：ES 删不掉只记 WARN，残留文档
// 在回表重排时被跳过，重跑 seed / reindex 会收敛。
func TestDelete_ESFailureDoesNotBlock(t *testing.T) {
	next := mocks.NewMockRepository(t)
	searcher := &fakeSearcher{deleteErr: errors.New("es: timeout")}
	next.EXPECT().Delete(mock.Anything, uint64(7)).Return(nil)

	repo := NewSearchRepository(next, searcher, zaptest.NewLogger(t))
	require.NoError(t, repo.Delete(context.Background(), 7))
	assert.Equal(t, []uint64{7}, searcher.deleted)
}

// TestSearchByKeyword_ReordersByRecallOrder 是"召回+回表"架构的核心用例：
// WHERE id IN (...) 不保证顺序（MockRepository 故意按主键序返回），
// 装饰器必须把结果重排回 ES 的 _score 相关度顺序——不重排的话，
// 用户搜到的"最相关商品"会消失在列表中间。
func TestSearchByKeyword_ReordersByRecallOrder(t *testing.T) {
	next := mocks.NewMockRepository(t)
	// ES 的相关度排序：3 最相关，1 次之，2 最后。
	searcher := &fakeSearcher{ids: []uint64{3, 1, 2}, total: 3}
	// 回表故意按主键序返回（模拟 MySQL 的实际行为）。
	next.EXPECT().ListByIDs(mock.Anything, []uint64{3, 1, 2}).Return([]*model.Product{
		{ID: 1, Name: "降噪耳机"},
		{ID: 2, Name: "耳机收纳盒"},
		{ID: 3, Name: "蓝牙耳机"},
	}, nil)

	repo := NewSearchRepository(next, searcher, zaptest.NewLogger(t))
	products, total, err := repo.SearchByKeyword(context.Background(), "耳机", 1, 20)

	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, products, 3)
	assert.Equal(t, uint64(3), products[0].ID, "必须保持 ES 的相关度顺序")
	assert.Equal(t, uint64(1), products[1].ID)
	assert.Equal(t, uint64(2), products[2].ID)
}

// TestSearchByKeyword_FallbackOnESFailure 验证降级：ES 召回失败时
// 退回下一层的 LIKE 实现，搜索是浏览路径，"搜得粗糙"好过"搜不了"。
func TestSearchByKeyword_FallbackOnESFailure(t *testing.T) {
	next := mocks.NewMockRepository(t)
	searcher := &fakeSearcher{err: errors.New("connection refused")}
	// 降级后走的是下一层的 SearchByKeyword（gorm LIKE 实现）。
	next.EXPECT().SearchByKeyword(mock.Anything, "耳机", 1, 20).Return([]*model.Product{
		{ID: 1, Name: "蓝牙耳机"},
	}, int64(1), nil)

	repo := NewSearchRepository(next, searcher, zaptest.NewLogger(t))
	products, total, err := repo.SearchByKeyword(context.Background(), "耳机", 1, 20)

	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, products, 1)
	assert.Equal(t, "蓝牙耳机", products[0].Name)
}

// TestSearchByKeyword_EmptyRecall 召回为空时短路返回，不打回表这一枪
// （mock 未设 ListByIDs 期望，调了就算失败）。
func TestSearchByKeyword_EmptyRecall(t *testing.T) {
	next := mocks.NewMockRepository(t)
	searcher := &fakeSearcher{ids: []uint64{}, total: 0}

	repo := NewSearchRepository(next, searcher, zaptest.NewLogger(t))
	products, total, err := repo.SearchByKeyword(context.Background(), "不存在的商品", 1, 20)

	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, products)
}

// TestSearchByKeyword_LIKE 验证 gorm 的 LIKE 兜底实现（sqlite 真实 SQL）：
// 名称包含关键词的商品被命中，不相关的被过滤，total 与分页正确。
func TestSearchByKeyword_LIKE(t *testing.T) {
	repo := NewGormRepository(newSQLiteDB(t))
	seedProduct(t, repo, "蓝牙耳机", 19900, 10)
	seedProduct(t, repo, "降噪耳机", 29900, 5)
	seedProduct(t, repo, "机械键盘", 39900, 3)

	products, total, err := repo.SearchByKeyword(context.Background(), "耳机", 1, 20)

	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, products, 2)
}

// TestSearchByKeyword_LIKEEscapesWildcards 是转义的核心用例：
// 用户输入 "100%" 里的 % 必须被当成字面量，而不是 LIKE 的任意匹配符——
// 不转义的话这个搜索会命中全表。
func TestSearchByKeyword_LIKEEscapesWildcards(t *testing.T) {
	repo := NewGormRepository(newSQLiteDB(t))
	seedProduct(t, repo, "蓝牙耳机", 19900, 10)
	seedProduct(t, repo, "机械键盘", 39900, 3)

	products, total, err := repo.SearchByKeyword(context.Background(), "100%", 1, 20)

	require.NoError(t, err)
	assert.Equal(t, int64(0), total, "% 必须按字面量匹配，不能命中任何商品")
	assert.Empty(t, products)
}

// TestListByIDs 验证回表批量查询：只取目标 id，空输入短路不打 SQL。
func TestListByIDs(t *testing.T) {
	repo := NewGormRepository(newSQLiteDB(t))
	p1 := seedProduct(t, repo, "蓝牙耳机", 19900, 10)
	seedProduct(t, repo, "机械键盘", 39900, 3)
	p3 := seedProduct(t, repo, "降噪耳机", 29900, 5)

	products, err := repo.ListByIDs(context.Background(), []uint64{p1.ID, p3.ID})

	require.NoError(t, err)
	require.Len(t, products, 2)
	ids := []uint64{products[0].ID, products[1].ID}
	assert.Contains(t, ids, p1.ID)
	assert.Contains(t, ids, p3.ID)

	empty, err := repo.ListByIDs(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}
