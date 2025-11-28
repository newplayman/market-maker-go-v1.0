# 构建阶段
FROM golang:1.23-alpine AS builder

WORKDIR /build

# 安装依赖
RUN apk add --no-cache git make

# 复制go mod文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 构建
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o phoenix cmd/runner/main.go

# 运行阶段
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /build/phoenix .

# 创建数据目录
RUN mkdir -p /app/data

# 暴露Prometheus端口
EXPOSE 9090

# 运行
ENTRYPOINT ["./phoenix"]
CMD ["-config", "config.yaml", "-log", "info"]
