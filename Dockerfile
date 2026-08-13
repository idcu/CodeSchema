# CodeSchema Dockerfile
# 多阶段构建：build → runtime

# === 构建阶段 ===
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=1 go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /build/codeschema \
    ./cmd/codeschema

# === 运行阶段 ===
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /build/codeschema .

EXPOSE 8080 8081

ENTRYPOINT ["/app/codeschema"]
CMD ["serve", "--http", ":8081"]