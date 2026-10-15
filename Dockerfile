# 多阶段构建：先构建前端，再编译 Go 二进制（前端产物经 //go:embed 打进二进制）。
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/quotahub-go ./cmd/quotahub-go

FROM alpine:3.20
RUN adduser -D -u 10001 quotahub
USER quotahub
WORKDIR /app
COPY --from=build /out/quotahub-go /app/quotahub-go
EXPOSE 19250
ENTRYPOINT ["/app/quotahub-go"]
