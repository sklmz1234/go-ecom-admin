# go-ecom-admin 开发环境常用操作。
# 前置：cp .env.example .env（compose 自动读取同目录 .env）

COMPOSE := docker compose
K8S_DIR := deploy/k8s

.PHONY: help up down restart logs ps build seed clean k8s-build k8s-apply k8s-delete k8s-seed k8s-status

help: ## 显示本帮助
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-10s %s\n", $$1, $$2}'

up: ## 构建镜像并后台启动整套环境
	$(COMPOSE) up -d --build

down: ## 停止并移除容器（数据卷保留，数据不丢）
	$(COMPOSE) down

restart: ## 重启三个 Go 服务（不动 MySQL 和数据）
	$(COMPOSE) restart user-service product-service api-gateway

logs: ## 跟踪全部服务日志（Ctrl-C 退出，不影响运行中的服务）
	$(COMPOSE) logs -f --tail=100

ps: ## 查看各容器状态
	$(COMPOSE) ps

build: ## 只构建镜像不启动
	$(COMPOSE) build

seed: ## 灌入测试数据（10 用户 + 20 商品，用户密码均为 123456）
	$(COMPOSE) run --rm seed

clean: ## 停止并删除容器和数据卷（数据会清零，慎用）
	$(COMPOSE) down -v

k8s-build: ## 构建全部镜像（kind 集群经 containerd 镜像存储可直接使用）
	$(COMPOSE) build

k8s-apply: ## 部署整套到 K8s（namespace ecom；首次先 make k8s-build）
	kubectl apply -f $(K8S_DIR)

k8s-delete: ## 删除 K8s 部署（StatefulSet 的 PVC 保留，MySQL 数据不丢）
	kubectl delete -f $(K8S_DIR)

k8s-seed: ## 灌入测试数据（先等 MySQL 就绪；Job 不可重入，删旧建新）
	kubectl wait --for=condition=ready pod/mysql-0 -n ecom --timeout=180s
	kubectl delete job seed -n ecom --ignore-not-found
	kubectl apply -f $(K8S_DIR)/30-seed-job.yaml
	@echo "--- seed 输出 ---"
	kubectl wait --for=condition=complete job/seed -n ecom --timeout=180s
	kubectl logs -n ecom job/seed

k8s-status: ## 查看 ecom 命名空间全部资源状态
	kubectl get all -n ecom
