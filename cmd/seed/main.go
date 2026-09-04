// Command seed 给本地开发环境灌测试数据：10 个用户 + 20 个商品。
//
// 设计决策：
//   - 复用 internal/user/model 和 internal/product/model 里已有的 GORM 模型，
//     不新建一套"仅供 seed 用"的表结构——测试数据必须长在真实的表上，否则
//     跑起来的服务和种子数据对不上，这个脚本就失去了意义。
//   - 每次运行先 TRUNATE 再插入：种子脚本的核心诉求是"给我一个确定的初始状态"，
//     而不是"追加一批数据"。反复运行不应该报主键/唯一索引冲突，也不应该让
//     数据量越滚越大。
//   - 密码哈希方式（bcrypt.DefaultCost）和 internal/user/service.Register 保持
//     一致：种子用户必须能用同样的登录接口验证密码，如果这里随便换一种哈希
//     方式，种子生成的用户就登录不了，起不到"能跑通完整流程"的测试数据作用。
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"go-ecom-admin/pkg/config"
	"go-ecom-admin/pkg/database"

	productmodel "go-ecom-admin/internal/product/model"
	usermodel "go-ecom-admin/internal/user/model"
)

// seedPassword 是所有种子用户统一使用的明文密码，仅用于本地开发/联调登录，
// 不用于任何真实环境——这一点足够重要，专门写一行注释而不是让人猜。
const seedPassword = "123456"

// productNames 用真实商品名而不是 "product-1" 这种占位符，是为了让本地联调时
// 看到的列表页/详情页尽量接近真实产品的观感，便于顺带发现前端展示上的问题
// （比如商品名过长导致的排版截断）。
var productNames = []string{
	"蓝牙耳机", "机械键盘", "USB-C充电器", "无线鼠标", "27英寸显示器",
	"移动电源", "智能手表", "蓝牙音箱", "笔记本支架", "人体工学椅",
	"高清摄像头", "无线充电器", "游戏手柄", "降噪耳机", "机械硬盘",
	"固态硬盘", "千兆路由器", "网络交换机", "显卡散热器", "电竞椅垫",
}

func main() {
	configPath := flag.String("config", "configs/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	db, err := gorm.Open(mysql.Open(cfg.MySQL.DSN()), &gorm.Config{TranslateError: true}) // TranslateError: 驱动方言错误(如 MySQL 1062)→gorm.ErrDuplicatedKey 等统一错误
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect mysql: %v\n", err)
		os.Exit(1)
	}

	// 带锁迁移：seed 可能和业务服务同时启动（如 make k8s-seed 在
	// Deployment 滚动起来时跑），同样要走命名锁避免抢建表。
	if err := database.Migrate(db, 30*time.Second, &usermodel.User{}, &productmodel.Product{}); err != nil {
		fmt.Fprintf(os.Stderr, "auto migrate: %v\n", err)
		os.Exit(1)
	}

	// TRUNCATE 而不是 DELETE FROM：顺带把 AUTO_INCREMENT 计数器归零，
	// 保证每次跑完种子脚本后 ID 都是从 1 开始的确定值，方便联调时直接用
	// 固定 ID（比如 GET /api/v1/products/1）而不用现查一遍列表。
	if err := db.Exec("TRUNCATE TABLE users").Error; err != nil {
		fmt.Fprintf(os.Stderr, "truncate users: %v\n", err)
		os.Exit(1)
	}
	if err := db.Exec("TRUNCATE TABLE products").Error; err != nil {
		fmt.Fprintf(os.Stderr, "truncate products: %v\n", err)
		os.Exit(1)
	}

	// 10 个种子用户密码都一样，哈希只需要算一次——bcrypt 本身带随机 salt，
	// 复用同一份哈希结果不会让这些用户的密码"看起来一样"，反而省掉 9 次
	// 重复的哈希计算（bcrypt 默认 cost 下单次哈希有意做得比较慢）。
	hash, err := bcrypt.GenerateFromPassword([]byte(seedPassword), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash password: %v\n", err)
		os.Exit(1)
	}

	users := make([]*usermodel.User, 0, 10)
	for i := 1; i <= 10; i++ {
		users = append(users, &usermodel.User{
			Username:     fmt.Sprintf("user%d", i),
			Email:        fmt.Sprintf("user%d@example.com", i),
			PasswordHash: string(hash),
		})
	}
	if err := db.Create(&users).Error; err != nil {
		fmt.Fprintf(os.Stderr, "insert users: %v\n", err)
		os.Exit(1)
	}

	products := make([]*productmodel.Product, 0, len(productNames))
	for _, name := range productNames {
		products = append(products, &productmodel.Product{
			Name:       name,
			PriceCents: int64(1990 + rand.Intn(9990-1990+1)),
			Stock:      int32(rand.Intn(501)),
		})
	}
	if err := db.Create(&products).Error; err != nil {
		fmt.Fprintf(os.Stderr, "insert products: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("seeded users:")
	for _, u := range users {
		fmt.Printf("  id=%d username=%s (password=%s)\n", u.ID, u.Username, seedPassword)
	}

	fmt.Println("seeded products:")
	for _, p := range products {
		fmt.Printf("  id=%d name=%s price=%.2f元 stock=%d\n", p.ID, p.Name, float64(p.PriceCents)/100, p.Stock)
	}
}
