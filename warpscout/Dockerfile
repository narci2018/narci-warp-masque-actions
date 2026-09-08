FROM golang:1.26-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

# 配置国内 Go 模块代理，解决国内网络下 proxy.golang.org 连接超时问题
ENV GOPROXY=https://goproxy.cn,direct

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION#v}" -o /warpscout .

FROM alpine:latest

# 配置国内 Alpine 源并安装必要证书
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories && \
    apk add --no-cache ca-certificates tzdata sing-box

WORKDIR /data

COPY --from=build /warpscout /usr/local/bin/warpscout

# 开放冷门高位端口：WebUI (29880), SOCKS5 (29881)
EXPOSE 29880 29881

ENTRYPOINT ["warpscout"]
CMD ["web", "-l", "0.0.0.0:29880", "-P", "29881"]
