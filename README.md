# go-ecom-admin

我正在做的电商管理后台，也是我的第一个完整微服务项目。目标很直接：把它做成能写进简历、经得起面试追问的项目，所以里面每个设计决策我都尽量搞清楚"为什么"，而不是照着教程抄一遍。

## 项目是什么

三个服务：api-gateway、user-service、product-service。网关用 Gin 收 HTTP 请求，通过 gRPC 调两个下游服务，各自用 GORM 连 MySQL。选这个拓扑是因为它是微服务最经典的入门结构，能覆盖到服务间通信、认证、分层这些核心问题，又不会复杂到失控。

```
HTTP 客户端 → api-gateway (Gin, :8080)
                ├─ gRPC :9001 → user-service    → MySQL
                └─ gRPC :9002 → product-service → MySQL
```

技术栈：Go 1.25 / Gin / gRPC + Protobuf / GORM / JWT (golang-jwt/v5) + bcrypt / Viper / zap。

## 目录结构

```
cmd/          每个服务一个入口，main.go 只做依赖组装
configs/      config.yaml，本地开发默认值，生产用环境变量覆盖
internal/
  gateway/    router / middleware / handler / service / repository
  user/       model / repository / service
  product/    model / repository / service
pkg/          config、logger、errors、jwt，可复用的基础设施
proto/        .proto 文件和生成的代码
frontend/     一个 Vite 写的管理界面（在慢慢补）
```

## 怎么跑起来

### 方式一：Docker Compose（推荐）

一条命令起整套（MySQL + 3 个服务）：

```bash
cp .env.example .env   # 首次使用，可按需改密码/端口
make up
```

常用操作：

```bash
make seed    # 灌测试数据：10 个用户（密码均为 123456）+ 20 个商品
make logs    # 跟踪全部服务日志
make down    # 停止（数据卷保留，数据不丢）
make clean   # 停止并清空数据卷（数据清零）
```

实现要点：多阶段构建（golang:1.25-alpine 编译 → alpine 运行，`CGO_ENABLED=0` 静态编译）；
容器内靠环境变量覆盖 config.yaml 的 `127.0.0.1` 默认值（Viper `AutomaticEnv`），
服务间用 compose service 名寻址；MySQL 用 healthcheck 保证"真正就绪"后下游才启动。

### 方式二：本机直接跑

先准备一个 MySQL，建库：

```bash
mysql -uroot -proot -e "CREATE DATABASE IF NOT EXISTS ecom_admin"
```

三个终端分别启动：

```bash
go run ./cmd/user-service
go run ./cmd/product-service
go run ./cmd/api-gateway
```

试一下：

```bash
# 注册
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","email":"alice@example.com","password":"secret123"}'

# 登录拿 JWT
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"secret123"}'

# 带 token 查商品
curl http://localhost:8080/api/v1/products -H "Authorization: Bearer <token>"
```

config.yaml 里的 JWT secret 是开发用的默认值，生产环境记得用 `JWT_SECRET` 环境变量覆盖。

## 测试

```bash
go test ./...
```

六个包全绿，覆盖率在 66%~88% 之间。我分了两层来测：

- service 层用 mockery 生成的 MockRepository，只测业务规则本身——参数校验、bcrypt、防用户枚举、AppError 到 gRPC status 的翻译这些，不碰数据库。
- repository 层用 glebarez/sqlite（纯 Go 的内存 SQLite）跑真实的 GORM 行为，不需要本机装 MySQL。

接口改了以后重新生成 mock：`mockery`，配置在 `.mockery.yaml`。

## 几个我比较得意的设计点

写下来主要是为了面试的时候自己能讲清楚：

1. **Update/Delete 判存在性用 RowsAffected == 0**（product_repository.go）。一开始我是先查一遍再改，后来意识到这不仅多一次往返，还有 TOCTOU 竞态——查的时候在，改的时候可能没了。用受影响行数一次搞定。

2. **登录失败不区分"用户不存在"和"密码错误"**（user_service.go）。统一返回同一个错误，否则攻击者可以拿登录接口枚举出哪些用户名注册过。

3. **JWT Parse 强制 HMAC 算法白名单**（pkg/jwt）。不指定的话 golang-jwt 会接受 token header 里声明的算法，经典的 alg-confusion 攻击就能伪造 admin token。

4. **错误码协议无关**（pkg/errors）。业务错误定义在应用层，到网关翻译成 HTTP 状态码、在服务里翻译成 gRPC status。这样 SQL 报错细节也不会漏到客户端。

5. **GORM 开了 TranslateError: true**。踩过一个坑：不开这个开关，repository 里的 `errors.Is(err, gorm.ErrDuplicatedKey)` 永远不会命中，重复用户名注册会被当成 500 而不是 409。驱动的 1062 错误得让 GORM 先翻译成统一错误才好判。

6. **proto 和对外 DTO 的转换归 gateway**（gateway/model/dto.go）。相当于反腐层，领域服务不感知 HTTP 那边的字段格式和单位，改接口契约不会穿透到业务代码。

## 接下来想做

- 网关侧的 handler/service 层测试（现在主要覆盖了领域服务）
- 可观测性：接入统一 trace 和 metrics
- 限流、熔断（已经在别的 demo 里练过 sentinel，还没搬进来）
