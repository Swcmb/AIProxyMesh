# syntax=docker/dockerfile:1

ARG NODE_IMAGE=node:24-alpine
ARG GO_IMAGE=golang:1.26.10-alpine
ARG RUNTIME_IMAGE=alpine:3.21

FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS webui
ARG NPM_REGISTRY=https://registry.npmmirror.com
WORKDIR /src
COPY webui/package.json webui/package-lock.json ./webui/
RUN cd webui && npm ci --no-audit --no-fund --registry=${NPM_REGISTRY}
COPY webui/ ./webui/
RUN cd webui && npm run build

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG GOPROXY=https://mirrors.aliyun.com/goproxy/,https://proxy.golang.org,direct
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
ENV GOPROXY=${GOPROXY} CGO_ENABLED=0 GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN for attempt in 1 2 3; do         go mod download && exit 0;         echo "go mod download attempt ${attempt} failed; retrying" >&2;         sleep 5;     done;     exit 1
COPY . .
COPY --from=webui /src/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/mesh ./cmd/mesh

FROM ${RUNTIME_IMAGE}
RUN apk add --no-cache ca-certificates tzdata     && addgroup -S -g 65532 mesh     && adduser -S -D -u 65532 -G mesh mesh
USER mesh
COPY --from=build /out/mesh /mesh
EXPOSE 8080 8081
ENTRYPOINT ["/mesh"]
CMD ["-config", "/data/config.json"]
