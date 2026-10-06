# 备选部署方式：Vercel 之外，也可以打包成镜像跑在任何容器平台。
FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/wrxbpq ./cmd/wrxbpq

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/wrxbpq /app/wrxbpq
COPY public /app/public

ENV PORT=8080
EXPOSE 8080
CMD ["/app/wrxbpq", "serve", "--port", "8080"]
