FROM golang:1.26-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

ENV GOPROXY=https://goproxy.cn,direct

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION#v}" -o /warpscout .

FROM python:3.11-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl tzdata && rm -rf /var/lib/apt/lists/*

WORKDIR /data

COPY --from=build /warpscout /usr/local/bin/warpscout
COPY bin/aether /usr/local/bin/aether
COPY bin/psiphon-tunnel-core /usr/local/bin/psiphon-tunnel-core
COPY bin/sing-box /usr/local/bin/sing-box
COPY bin/server_entries.txt /data/server_entries.txt

RUN chmod +x /usr/local/bin/warpscout /usr/local/bin/aether /usr/local/bin/psiphon-tunnel-core /usr/local/bin/sing-box

# 开放冷门高位端口：WebUI (29880), SOCKS5 (29881), 以及25国多选专属端口 (29891-29915)
EXPOSE 29880 29881 29891-29915

ENTRYPOINT ["warpscout"]
CMD ["web", "-l", "0.0.0.0:29880", "-P", "29881"]
