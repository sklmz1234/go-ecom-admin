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

// fakeSearcher 是 ProductSearcher 的测试替身：只实现 Search 的行为录制，
// Index/Delete 在查询链路测试里用不到。
type fakeSearcher struct {
	ids   []uint64
	total int64
	err   error
}

func (f *fakeSearcher) Search(_ context.Context, _ string, _, _ int) ([]uint64, int64, error) {
	return f.ids, f.total, f.err
}

func (f *fakeSearcher) Index(_ context.Context, _ *model.Product) error { return nil }
func (f *fakeSearcher) Delete(_ context.Context, _ uint64) error        { return nil }

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
