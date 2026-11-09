# ---------- 构建阶段 ----------
FROM golang:1.25-alpine AS builder
WORKDIR /src

# 先拷贝 go.mod（本项目零第三方依赖，无需 go mod download）
COPY go.mod ./
COPY main.go ./

# 静态编译，去掉调试信息，体积最小
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/table-editor .

# ---------- 运行阶段 ----------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 app

ENV TZ=Asia/Shanghai

# 数据目录：data.json 就放在这里，挂载卷即可持久化
RUN mkdir -p /app/data && chown -R app:app /app

COPY --from=builder /out/table-editor /usr/local/bin/table-editor

USER app
WORKDIR /app/data
VOLUME ["/app/data"]

EXPOSE 8080
ENTRYPOINT ["table-editor", "8080"]
