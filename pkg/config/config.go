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
	App        AppConfig        `mapstructure:"app"`
	Log        LogConfig        `mapstructure:"log"`
	MySQL      MySQLConfig      `mapstructure:"mysql"`
	Server     ServerConfig     `mapstructure:"server"`
	GRPCClient GRPCClientConfig `mapstructure:"grpc_client"`
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

// ServerConfig 把三个入口各自的监听端口放在一起，方便一份 config.yaml
// 同时描述全部服务的部署形态（这在 docker-compose / k8s ConfigMap 场景下很常见）。
type ServerConfig struct {
	APIGateway     HTTPServerConfig `mapstructure:"api_gateway"`
	UserService    GRPCServerConfig `mapstructure:"user_service"`
	ProductService GRPCServerConfig `mapstructure:"product_service"`
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
