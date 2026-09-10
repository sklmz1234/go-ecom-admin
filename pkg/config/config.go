// Package config 集中管理应用配置。
//
// 设计决策：
//  1. 用 Viper 而不是 flag/os.Getenv 手写解析，是因为 Viper 原生支持
//     "文件默认值 + 环境变量覆盖" 的组合，这是微服务在开发机和容器环境
//     之间切换配置的标准做法（本地用 yaml，线上用容器注入的环境变量覆盖敏感字段）。
//  2. Load 返回结构体而不是暴露全局 viper 实例，是为了让调用方在编译期
//     就能发现拼写错误的配置字段（结构体字段 vs viper.GetString("key") 的字符串 key）。
package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config 是整个应用的配置根节点，字段与 configs/config.yaml 的顶层 key 一一对应。
type Config struct {
	App           AppConfig           `mapstructure:"app"`
	Log           LogConfig           `mapstructure:"log"`
	MySQL         MySQLConfig         `mapstructure:"mysql"`
	Redis         RedisConfig         `mapstructure:"redis"`
	Elasticsearch ElasticsearchConfig `mapstructure:"elasticsearch"`
	JWT           JWTConfig           `mapstructure:"jwt"`
	Server        ServerConfig        `mapstructure:"server"`
	GRPCClient    GRPCClientConfig    `mapstructure:"grpc_client"`
	Telemetry     TelemetryConfig     `mapstructure:"telemetry"`
}

type AppConfig struct {
	Name string `mapstructure:"name"`
	Env  string `mapstructure:"env"`
}

// LogConfig 直接映射到 pkg/logger 的初始化参数，配置和日志两个包解耦，
// logger 包不需要知道 Viper 的存在。
type LogConfig struct {
	Level      string   `mapstructure:"level"`
	Encoding   string   `mapstructure:"encoding"`
	OutputPath []string `mapstructure:"output_paths"`
}

type MySQLConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	User         string `mapstructure:"user"`
	Password     string `mapstructure:"password"`
	DBName       string `mapstructure:"db_name"`
	Charset      string `mapstructure:"charset"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
}

// DSN 拼接 GORM mysql driver 需要的连接字符串，避免每个 main.go 重复拼接逻辑。
func (m MySQLConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=True&loc=Local",
		m.User, m.Password, m.Host, m.Port, m.DBName, m.Charset)
}

// RedisConfig 目前只有 product-service 的读缓存在用，但放在公共配置里——
// 后续 order-service 的库存缓存也会读同一份（同一个 Redis 实例，不同 key 前缀）。
// Addr 用 host:port 单字段而不是拆两个，因为 go-redis 原生就吃这个格式，
// 环境变量覆盖（REDIS_ADDR）也只需一个值。
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// ElasticsearchConfig 是 product-service 商品搜索（阶段 5A）的连接配置。
// Addr 带 scheme（http://...），因为 go-elasticsearch 的 Addresses 吃完整 URL；
// Index 独立成配置而不是写死常量，方便测试环境用另一个索引名互相隔离。
// 环境变量覆盖：ELASTICSEARCH_ADDR / ELASTICSEARCH_INDEX。
type ElasticsearchConfig struct {
	Addr  string `mapstructure:"addr"`
	Index string `mapstructure:"index"`
}

// JWTConfig 是 user-service（签发）和 api-gateway（校验）共用的一份配置——
// 两个服务必须用同一个 secret，否则 gateway 校验不了 user-service 签的 token。
// Secret 只在 configs/config.yaml 里放一个开发用的默认值，生产环境要靠
// Viper 的环境变量覆盖机制（JWT_SECRET）换成真正的密钥，不能把生产密钥提交进仓库。
type JWTConfig struct {
	Secret      string `mapstructure:"secret"`
	ExpireHours int    `mapstructure:"expire_hours"`
}

// ServerConfig 把三个入口各自的监听端口放在一起，方便一份 config.yaml
// 同时描述全部服务的部署形态（这在 docker-compose / k8s ConfigMap 场景下很常见）。
type ServerConfig struct {
	APIGateway     HTTPServerConfig `mapstructure:"api_gateway"`
	UserService    GRPCServerConfig `mapstructure:"user_service"`
	ProductService GRPCServerConfig `mapstructure:"product_service"`
	OrderService   GRPCServerConfig `mapstructure:"order_service"`
}

type HTTPServerConfig struct {
	HTTPPort int `mapstructure:"http_port"`
}

type GRPCServerConfig struct {
	GRPCPort int `mapstructure:"grpc_port"`
}

// GRPCClientConfig 是 api-gateway 作为 gRPC 客户端时，下游服务的拨号地址。
// 单独成节而不是从 ServerConfig 里拼 "127.0.0.1:port"，是因为生产环境里
// 客户端连接的地址（服务发现名/域名）和服务端监听的地址往往不是同一个东西。
type GRPCClientConfig struct {
	UserServiceAddr    string `mapstructure:"user_service_addr"`
	ProductServiceAddr string `mapstructure:"product_service_addr"`
	OrderServiceAddr   string `mapstructure:"order_service_addr"`
}

// TelemetryConfig 是链路追踪（阶段 2D）的开关与端点配置。三个服务共用一份
// yaml，但 service 名不在这里——它由各 main.go 调 telemetry.Setup 时显式
// 传入（同一份配置文件要能描述三个不同服务，服务名是"进程身份"不是"环境属性"）。
// 环境变量覆盖（Viper AutomaticEnv，"." → "_"）：
//   - TELEMETRY_ENABLED=false          裸 go run 不起 Jaeger 时关掉追踪
//   - TELEMETRY_OTLP_ENDPOINT=jaeger:4317  容器网络内指向 compose 的 Jaeger
type TelemetryConfig struct {
	Enabled      bool    `mapstructure:"enabled"`
	OTLPEndpoint string  `mapstructure:"otlp_endpoint"`
	SampleRatio  float64 `mapstructure:"sample_ratio"`
}

// Load 从 path 指向的 yaml 文件加载配置，并允许同名环境变量覆盖
// （例如 MYSQL_PASSWORD 会覆盖 mysql.password）。
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)

	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("config: read config file %q: %w", path, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal config: %w", err)
	}

	return &cfg, nil
}
