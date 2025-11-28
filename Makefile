.PHONY: build run test clean deps lint

# 构建
build:
	go build -o bin/phoenix cmd/runner/main.go

# 构建应急工具
build-emergency:
	go build -o bin/emergency cmd/emergency/main.go

# 运行
run:
	go run cmd/runner/main.go -config config.yaml -log debug

# 测试
test:
	go test -v ./...

# 清理
clean:
	rm -rf bin/
	rm -rf data/

# 安装依赖
deps:
	go mod download
	go mod tidy

# 代码检查
lint:
	golangci-lint run

# 格式化代码
fmt:
	go fmt ./...

# 生成配置文件
config:
	cp config.yaml.example config.yaml

# Docker构建
docker-build:
	docker build -t phoenix:latest .

# Docker运行
docker-run:
	docker run -v $(PWD)/config.yaml:/app/config.yaml phoenix:latest
