# 多阶段构建：builder 编译出静态二进制，运行阶段只带产物，镜像从 ~1GB 缩到 ~25MB。
# 通过 --build-arg SERVICE=<name> 选择编译 cmd/ 下的哪个入口，
# docker-compose.yml 里每个服务传各自的 SERVICE，共用这一份 Dockerfile。
FROM golang:1.25-alpine AS builder

WORKDIR /app

# 容器内默认的 proxy.golang.org 国内不可达，换成 goproxy.cn（和本机 Go 环境一致）。
# 只影响构建阶段，不进最终镜像。
ENV GOPROXY=https://goproxy.cn,direct

# 先只拷依赖清单再 download：依赖不变时这两层命中缓存，
# 改业务代码不需要重新下载模块，是本地迭代速度的关键。
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG SERVICE=api-gateway
# CGO_ENABLED=0：纯静态编译，二进制不依赖系统 glibc，
# 才能直接跑在 alpine 这种精简基础镜像上。
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${SERVICE}

FROM alpine:3.20
# tzdata：让 TZ 环境变量生效（GORM DSN 的 loc=Local 依赖它）；
# ca-certificates：容器内发起 HTTPS 调用时的根证书，标准配置顺手带上。
RUN apk add --no-cache tzdata ca-certificates \
    && addgroup -S app && adduser -S app -G app

WORKDIR /app
COPY --from=builder /out/app /app/app
# 配置文件打进镜像：里面只有 127.0.0.1 的本地开发默认值，
# 容器环境靠 compose 注入的环境变量覆盖（Viper AutomaticEnv）。
COPY configs/config.yaml /app/configs/config.yaml

ENV TZ=Asia/Shanghai
# 非 root 运行：容器内提权攻击面更小，生产部署的基本功。
USER app

ENTRYPOINT ["/app/app"]
