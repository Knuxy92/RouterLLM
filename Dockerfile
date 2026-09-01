# syntax=docker/dockerfile:1

FROM node:22-alpine AS web-builder
WORKDIR /src/web
RUN npm install -g pnpm@11
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /src/internal/admin/dist ./internal/admin/dist
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o /out/routerllm ./cmd/routerllm

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/routerllm .
RUN chown -R 65532:65532 /app
EXPOSE 1765
USER 65532:65532
ENTRYPOINT ["./routerllm"]
