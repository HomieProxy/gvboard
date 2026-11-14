# Gvboard 镜像（多阶段构建：前端 → 后端 → 运行环境）
#   docker build -t gvboard:latest .
# 国内网络可指定镜像源：
#   docker build -t gvboard:latest --build-arg GOPROXY=https://goproxy.cn,direct --build-arg NPM_REGISTRY=https://registry.npmmirror.com .

ARG NODE_IMAGE=node:22-alpine
ARG GO_IMAGE=golang:1.26-alpine
ARG RUN_IMAGE=alpine:3.24

# ---------- 前端：管理端与内置默认主题 ----------
FROM ${NODE_IMAGE} AS web
ARG NPM_REGISTRY=https://registry.npmjs.org
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
COPY web/shared/package.json shared/
COPY web/admin/package.json admin/
COPY web/user/package.json user/
RUN npm ci --registry=${NPM_REGISTRY} --no-audit --no-fund
COPY web/ ./
RUN npm run build && npm run check:fingerprint

# ---------- 后端 ----------
FROM ${GO_IMAGE} AS build
ARG GOPROXY=https://proxy.golang.org,direct
ARG VERSION=dev
ENV GOPROXY=${GOPROXY} GOTOOLCHAIN=local CGO_ENABLED=0 GOFLAGS=-buildvcs=false
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY pkg/ pkg/
COPY plugins/ plugins/
COPY internal/ internal/
COPY --from=web /src/internal/web/admin/ internal/web/admin/
COPY --from=web /src/internal/theme/builtin/default/ internal/theme/builtin/default/
RUN go build -trimpath \
    -ldflags "-s -w -X gvboard/internal/cli.Version=${VERSION} -X gvboard/internal/cli.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /out/gvboard ./cmd/gvboard

# ---------- 运行环境 ----------
FROM ${RUN_IMAGE}
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 gvboard \
    && mkdir -p /app/data/themes \
    && chown -R gvboard:gvboard /app/data
COPY --from=build /out/gvboard /usr/local/bin/gvboard
USER gvboard
# 配置文件 config.yaml、外部主题目录 themes 都在数据目录中
WORKDIR /app/data
VOLUME ["/app/data"]
EXPOSE 8080
ENTRYPOINT ["gvboard"]
CMD ["serve"]
