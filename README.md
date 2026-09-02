REST 客户端 / 前端
HTTP/JSON :8080
api-gateway（cmd/api-gateway）
router
middleware
JWT 校验
handler
service
repo
gRPC 客户端
internal/gateway：router → middleware → handler → service → repository（client.go）
gRPC :9001
gRPC :9002
user-service（cmd/user-service）
service
bcrypt + JWT
repository
GORM
implements userpb.UserServiceServer
product-service（cmd/product-service）
service
校验+转换
repository
GORM
implements productpb.ProductServiceServer
MySQL · users 表
password_hash 不出服务
MySQL · products 表
price_cents 用分存
共享层：pkg/config(Viper) · pkg/logger(zap) · pkg/jwt · pkg/errors　｜　契约层：proto/user · proto/product（protoc 生成
