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
//   - 收尾同步衍生存储：清 Redis 读缓存（FlushDB）+ 重建 ES 搜索索引
//     （Recreate 后全量写入）。"确定的初始状态"必须包含所有衍生数据，
//     否则旧缓存/旧索引里的 id 和 TRUNCATE 后的新库对不上。
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"go-ecom-admin/pkg/cache"
	"go-ecom-admin/pkg/config"
	"go-ecom-admin/pkg/database"

	productmodel "go-ecom-admin/internal/product/model"
	productrepo "go-ecom-admin/internal/product/repository"
	usermodel "go-ecom-admin/internal/user/model"
)

// seedPassword 是所有种子用户统一使用的明文密码，仅用于本地开发/联调登录，
// 不用于任何真实环境——这一点足够重要，专门写一行注释而不是让人猜。
const seedPassword = "123456"

// seedProduct 把商品名和图文信息绑在一条记录里（阶段 5A：C 端首页直接展示
// description/image_url）。三者一体定义，扩商品时不会漏配图或漏描述。
// 图片用 picsum 外链占位（按英文 seed 名出固定图），本地不用准备静态资源；
// description 里刻意埋了搜索关键词（如"耳机""充电"），方便联调 ES 召回。
type seedProduct struct {
	name        string
	description string
	imageURL    string
}

// productNames 用真实商品名而不是 "product-1" 这种占位符，是为了让本地联调时
// 看到的列表页/详情页尽量接近真实产品的观感，便于顺带发现前端展示上的问题
// （比如商品名过长导致的排版截断）。
var seedProducts = []seedProduct{
	{"蓝牙耳机", "蓝牙 5.3 真无线耳机，支持主动降噪，单次续航 8 小时，配充电盒可达 32 小时。", "https://picsum.photos/seed/bluetooth-earbuds/400/300"},
	{"机械键盘", "87 键热插拔机械键盘，Gasket 结构，三模连接，RGB 背光全键无冲。", "https://picsum.photos/seed/mechanical-keyboard/400/300"},
	{"USB-C充电器", "65W 氮化镓充电器，双 C 口快充，兼容手机耳机笔记本，折叠插脚便携。", "https://picsum.photos/seed/usb-c-charger/400/300"},
	{"无线鼠标", "2.4G/蓝牙双模无线鼠标，静音微动，人体工学握感，一节电池用半年。", "https://picsum.photos/seed/wireless-mouse/400/300"},
	{"27英寸显示器", "27 英寸 2K 165Hz IPS 显示器，95% DCI-P3 色域，升降旋转支架，办公游戏两相宜。", "https://picsum.photos/seed/monitor-27/400/300"},
	{"移动电源", "20000mAh 大容量移动电源，22.5W 双向快充，数显电量，可上飞机。", "https://picsum.photos/seed/power-bank/400/300"},
	{"智能手表", "1.43 英寸 AMOLED 智能手表，血氧心率监测，百种运动模式，14 天长续航。", "https://picsum.photos/seed/smart-watch/400/300"},
	{"蓝牙音箱", "便携蓝牙音箱，IPX7 防水，360° 环绕出声，露营浴室都能用，续航 24 小时。", "https://picsum.photos/seed/bluetooth-speaker/400/300"},
	{"笔记本支架", "铝合金笔记本支架，六档高度调节，镂空散热，折叠后仅一本书厚。", "https://picsum.photos/seed/laptop-stand/400/300"},
	{"人体工学椅", "人体工学电脑椅，4D 扶手 + 腰托独立调节，全网透气，久坐办公护腰首选。", "https://picsum.photos/seed/ergonomic-chair/400/300"},
	{"高清摄像头", "2K 高清摄像头，自动对焦 + 双降噪麦克风，网课视频会议画质清晰。", "https://picsum.photos/seed/hd-webcam/400/300"},
	{"无线充电器", "15W 磁吸无线充电器，手机耳机都能充，带散热风扇，夜间可当支架。", "https://picsum.photos/seed/wireless-charger/400/300"},
	{"游戏手柄", "多平台游戏手柄，霍尔摇杆永不漂移，六轴体感，PC/Switch/手机通吃。", "https://picsum.photos/seed/game-controller/400/300"},
	{"降噪耳机", "头戴式主动降噪耳机，-45dB 深度降噪，40mm 大动圈，通勤出差隔绝喧嚣。", "https://picsum.photos/seed/anc-headphones/400/300"},
	{"机械硬盘", "4TB 企业级机械硬盘，7200 转 CMR 垂直记录，NAS 仓储备份大容量之选。", "https://picsum.photos/seed/hdd-4tb/400/300"},
	{"固态硬盘", "1TB NVMe 固态硬盘，PCIe 4.0 读速 7000MB/s，带独立缓存，游戏秒加载。", "https://picsum.photos/seed/nvme-ssd/400/300"},
	{"千兆路由器", "Wi-Fi 6 千兆路由器，四天线穿墙，Mesh 组网支持，全屋信号无死角。", "https://picsum.photos/seed/wifi6-router/400/300"},
	{"网络交换机", "8 口千兆网络交换机，即插即用免配置，金属外壳散热好，家用小型办公室适用。", "https://picsum.photos/seed/network-switch/400/300"},
	{"显卡散热器", "显卡散热伴侣，双风扇下压式辅助散热，RGB 神光同步，降温降噪两不误。", "https://picsum.photos/seed/gpu-cooler/400/300"},
	{"电竞椅垫", "电竞椅专用坐垫靠垫套装，记忆棉慢回弹，久坐不累，适配大多数椅子。", "https://picsum.photos/seed/chair-cushion/400/300"},
}

func main() {
	configPath := flag.String("config", "configs/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	// 带退避重试的连接（理由同业务服务）：seed 是手动跑的工具，经常在
	// MySQL 还在启动/重启的窗口期被执行，秒退只会逼人手动重跑。
	// TranslateError: 驱动方言错误(如 MySQL 1062)→gorm.ErrDuplicatedKey 等统一错误
	db, err := database.ConnectWithRetry(context.Background(), func() (*gorm.DB, error) {
		return gorm.Open(mysql.Open(cfg.MySQL.DSN()), &gorm.Config{TranslateError: true})
	}, database.ConnectConfig{})
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

	// TRUNCATE 只清了 MySQL，Redis 里还可能缓存着旧商品 JSON（2C 的读缓存，
	// TTL 30 分钟）。不清掉的话，product-service 会从缓存读出没有 owner_id 的
	// 旧数据，归属校验把所有写请求误判成 403——"确定的初始状态"必须包含缓存。
	// 用 FlushDB 而不是按前缀删：seed 是开发工具，这个 Redis 实例专属于本项目；
	// 如果将来共享实例，这里要换成 SCAN product:* 逐批删。
	rdb, err := cache.New(cache.Config{Addr: cfg.Redis.Addr, Password: cfg.Redis.Password, DB: cfg.Redis.DB})
	if err != nil {
		// 清缓存是卫生措施不是核心职责，Redis 连不上只警告不中断
		// （比如本地裸跑 seed 而 Redis 没起时，库表重置仍然有用）。
		fmt.Fprintf(os.Stderr, "warn: connect redis, cache not flushed: %v\n", err)
	} else {
		if err := rdb.FlushDB(context.Background()).Err(); err != nil {
			fmt.Fprintf(os.Stderr, "warn: flush redis: %v\n", err)
		}
		_ = rdb.Close()
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

	products := make([]*productmodel.Product, 0, len(seedProducts))
	for i, sp := range seedProducts {
		products = append(products, &productmodel.Product{
			Name:        sp.name,
			PriceCents:  int64(1990 + rand.Intn(9990-1990+1)),
			Stock:       int32(rand.Intn(501)),
			// 商品轮流归属到 10 个种子用户，联调时任意登录一个账号
			// 都能改/删到"自己的"商品，也能撞到别人的商品验证 403。
			OwnerID:     users[i%len(users)].ID,
			Description: sp.description,
			ImageURL:    sp.imageURL,
		})
	}
	if err := db.Create(&products).Error; err != nil {
		fmt.Fprintf(os.Stderr, "insert products: %v\n", err)
		os.Exit(1)
	}

	// 全量重建 ES 索引（阶段 5A）：MySQL 刚被 TRUNCATE、自增 id 归零重用，
	// 旧索引里的文档必然对不上，必须先删索引重建再全量写入——seed 的语义是
	// "确定的初始状态"，ES 索引也是状态的一部分（和上面 FlushDB 清缓存同理）。
	// ES 连不上只警告不中断（同 Redis 的取舍）：查询侧会降级 LIKE，主流程
	// 不受影响，但输出里会明确打出"搜索索引未重建"。
	esSearcher, err := productrepo.NewESSearcher(cfg.Elasticsearch.Addr, cfg.Elasticsearch.Index, zap.NewExample())
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: connect elasticsearch, search index not rebuilt: %v\n", err)
	} else if err := esSearcher.Recreate(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "warn: recreate elasticsearch index: %v\n", err)
	} else {
		indexed := 0
		for _, p := range products {
			// 单条失败不中断全量灌入：记录下来继续，最后让计数说话。
			if err := esSearcher.Index(context.Background(), p); err != nil {
				fmt.Fprintf(os.Stderr, "warn: es index product id=%d: %v\n", p.ID, err)
				continue
			}
			indexed++
		}
		fmt.Printf("elasticsearch: index %q recreated, %d/%d products indexed\n",
			cfg.Elasticsearch.Index, indexed, len(products))
	}

	fmt.Println("seeded users:")
	for _, u := range users {
		fmt.Printf("  id=%d username=%s (password=%s)\n", u.ID, u.Username, seedPassword)
	}

	fmt.Println("seeded products:")
	for _, p := range products {
		fmt.Printf("  id=%d name=%s price=%.2f元 stock=%d owner_id=%d\n", p.ID, p.Name, float64(p.PriceCents)/100, p.Stock, p.OwnerID)
	}
}
