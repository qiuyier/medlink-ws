.PHONY: help up down restart logs ps clean init test kafka-init env-check

# 加载环境变量
include deployments/.env
export

help: ## 显示帮助信息
	@echo "MedLink WebSocket 服务管理命令"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

env-check: ## 检查环境变量
	@echo "检查环境变量配置..."
	@if [ ! -f deployments/.env ]; then \
		echo "❌ 错误: deployments/.env 文件不存在"; \
		exit 1; \
	fi
	@echo "✅ 环境变量文件存在"
	@echo "项目名称: $(COMPOSE_PROJECT_NAME)"
	@echo "环境: $(ENVIRONMENT)"

up: env-check ## 启动所有服务
	cd deployments && docker-compose up -d
	@echo "等待服务启动..."
	@sleep 5
	@make ps

down: ## 停止所有服务
	cd deployments && docker-compose down

restart: ## 重启所有服务
	cd deployments && docker-compose restart

logs: ## 查看所有日志
	cd deployments && docker-compose logs -f

logs-ws: ## 查看 WebSocket 服务日志
	cd deployments && docker-compose logs -f ws-server

logs-postgres: ## 查看 PostgreSQL 日志
	cd deployments && docker-compose logs -f postgres

logs-redis: ## 查看 Redis 日志
	cd deployments && docker-compose logs -f redis

logs-kafka: ## 查看 Kafka 日志
	cd deployments && docker-compose logs -f kafka

ps: ## 查看服务状态
	@echo "========== 服务状态 =========="
	cd deployments && docker-compose ps
	@echo ""
	@echo "========== 资源占用 =========="
	@docker stats --no-stream --format "table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}" | grep medlink

clean: ## 清理所有数据（危险！）
	@read -p "确定要删除所有数据吗？(yes/no): " confirm; \
	if [ "$$confirm" = "yes" ]; then \
		cd deployments && docker-compose down -v; \
		docker system prune -af --volumes; \
		echo "✅ 清理完成"; \
	else \
		echo "❌ 取消清理"; \
	fi

init: up ## 初始化数据库和 Kafka Topics
	@echo "初始化数据库..."
	@sleep 5
	@docker exec $(COMPOSE_PROJECT_NAME)-postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) -c "SELECT version();"
	@echo "✅ 数据库已就绪"
	@echo ""
	@echo "创建 Kafka Topics..."
	@bash scripts/init-kafka-topics.sh
	@echo "✅ 初始化完成"

test: ## 测试所有服务连接
	@echo "测试 PostgreSQL..."
	@docker exec $(COMPOSE_PROJECT_NAME)-postgres pg_isready -U $(POSTGRES_USER) || echo "❌ PostgreSQL 连接失败"
	@echo ""
	@echo "测试 Redis..."
	@docker exec $(COMPOSE_PROJECT_NAME)-redis redis-cli -a $(REDIS_PASSWORD) ping || echo "❌ Redis 连接失败"
	@echo ""
	@echo "测试 RabbitMQ..."
	@docker exec $(COMPOSE_PROJECT_NAME)-rabbitmq rabbitmqctl status | head -n 5 || echo "❌ RabbitMQ 连接失败"
	@echo ""
	@echo "测试 Kafka..."
	@docker exec $(COMPOSE_PROJECT_NAME)-kafka kafka-broker-api-versions --bootstrap-server localhost:9092 | head -n 1 || echo "❌ Kafka 连接失败"
	@echo ""
	@echo "✅ 所有服务测试完成"

kafka-topics: ## 列出 Kafka Topics
	docker exec $(COMPOSE_PROJECT_NAME)-kafka kafka-topics --bootstrap-server localhost:9092 --list

kafka-create: ## 手动创建 Kafka Topics
	bash scripts/init-kafka-topics.sh

redis-cli: ## 进入 Redis CLI
	docker exec -it $(COMPOSE_PROJECT_NAME)-redis redis-cli -a $(REDIS_PASSWORD)

psql: ## 进入 PostgreSQL
	docker exec -it $(COMPOSE_PROJECT_NAME)-postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB)

rabbitmq-status: ## 查看 RabbitMQ 状态
	docker exec $(COMPOSE_PROJECT_NAME)-rabbitmq rabbitmqctl status

monitoring-up: ## 启动监控服务
	cd deployments && docker-compose -f docker-compose.monitoring.yml up -d
	@echo "Prometheus: http://localhost:$(PROMETHEUS_PORT)"
	@echo "Grafana: http://localhost:$(GRAFANA_PORT)"

monitoring-down: ## 停止监控服务
	cd deployments && docker-compose -f docker-compose.monitoring.yml down

backup-db: ## 备份数据库
	@mkdir -p backups
	@docker exec $(COMPOSE_PROJECT_NAME)-postgres pg_dump -U $(POSTGRES_USER) $(POSTGRES_DB) > backups/backup_$$(date +%Y%m%d_%H%M%S).sql
	@echo "✅ 数据库备份完成: backups/backup_$$(date +%Y%m%d_%H%M%S).sql"

restore-db: ## 恢复数据库 (usage: make restore-db FILE=backups/backup_xxx.sql)
	@if [ -z "$(FILE)" ]; then \
		echo "❌ 错误: 请指定备份文件 (make restore-db FILE=backups/xxx.sql)"; \
		exit 1; \
	fi
	@docker exec -i $(COMPOSE_PROJECT_NAME)-postgres psql -U $(POSTGRES_USER) $(POSTGRES_DB) < $(FILE)
	@echo "✅ 数据库恢复完成"

build: ## 编译 Go 项目
	@echo "编译 WebSocket 服务..."
	@go build -o bin/medlink-ws cmd/server/main.go
	@echo "✅ 编译完成: bin/medlink-ws"

run: build ## 本地运行服务（不用 Docker）
	@./bin/medlink-ws -config=config.yaml

dev: ## 开发模式（热重载）
	@air -c .air.toml

install-tools: ## 安装开发工具
	@echo "安装开发工具..."
	@go install github.com/cosmtrek/air@latest
	@echo "✅ 工具安装完成"