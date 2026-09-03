# go-ecom-admin

基于 Go 微服务架构的电商管理后台，包含用户认证与商品管理两大领域。项目采用 api-gateway + 独立领域服务*的经典微服务拓扑，网关通过 gRPC 调用下游服务，各服务使用 GORM 访问 MySQL。
 技术栈


 Go 1.25 
HTTP 框架 Gin
RPC gRPC + Protobuf
 ORM GORM (MySQL)
 认证  JWT (golang-jwt/v5) + bcrypt 
配置  Viper（YAML + 环境变量覆盖）
 单元测试 testify + mockery（mock）+ glebarez/sqlite（纯 Go 内存库） 

 目录结构
go-ecom-admin/

 cmd/                        # 组合根：每个服务一个入口
 api-g  ateway/            # HTTP 网关 :8080
user-service/           # 用户服务 gRPC :9001
product-service/        # 商品服务 gRPC :9002
seed/                   # 数据库 seed 工具

configs/config.yaml         # 统一配置（dev 默认值，生产用环境变量覆盖）
 internal/
gateway/                # 网关：router / middleware / handler / service / repository / model
user/                   # 用户域：model / repository / service

 product/                # 商品域：model / repository / service
 
 pkg/                        # 可复用的基础设施包
 
 config/                 # Viper 封装，强类型 Config 结构体
 
 logger/                 # zap 封装
 
 errors/                 # 应用层错误码（协议无关）
 
jwt/                    # JWT 签发与解析
proto/                      # .proto 源文件与生成的 pb 代码
frontend/                   # 前端（Vite）




gateway/repository/client.go Repository 模式的泛化：网关侧 gRPC 客户端统一封装，service 层依赖接口而非具体实现 
 product_repository.go  Update/Delete 用 `RowsAffected == 0` 判存在性：省一次往返查询，且无 TOCTOU 竞态 
 user_service.go   登录失败统一返回相同错误，防用户枚举攻击（无法探测用户名是否存在） 
 pkg/errors   错误码协议无关 + 边缘翻译；错误分支不向客户端透传 SQL 细节 
 pkg/jwt Parse 强制 HMAC 算法白名单，防 alg-confusion 攻击 
 gateway/model/dto.go  反腐层：proto 与对外 DTO 之间的转换（含单位换算）归 gateway，领域服务不感知 HTTP 语义 
 GORM TranslateError: true驱动方言错误（如 MySQL 1062 唯一键冲突）统一翻译为 gorm.ErrDuplicatedKey，让 repository 层的 errors.Is 判定可靠命中，重复注册返回 409 而非 500 

 
 go gin grpc gorm viper mysql vite
restful网关
用户的电商后台管理，1浏览器发请求项目树中的router路由到具体函数，挂载中间件 2handler的parse解析参数，成功则将user id写入gin.context，不成功，则直接c.abort ，后面的不会执行3service转换，分到元，float64～int64，四舍五入精确，4repository的client，是一种泛化的处理，把grpc连接的操作当成数据库来泛化，然后请求序列化protobuf走grpc9002端口，然后是product的servise，解析参数proto至modle等，然后到repository调gorm的函数生成数据库指令操作然后反过来再来一遍。
其他补充，dto反腐层，modle的值对内服务和对外的不一样，middleware鉴权中间件，jwt显示传参，校验，viper日志封装，优雅停机，接口化注入mock假数据单元测试，vite前端解决跨域


