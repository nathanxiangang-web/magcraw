# magcraw —— 多阶段构建，产物约 10MB
FROM golang:1.23-alpine AS build
WORKDIR /src

COPY go.mod ./
COPY *.go ./
COPY web ./web

RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/magcraw .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 magcraw
WORKDIR /app
COPY --from=build /out/magcraw /app/magcraw
RUN mkdir -p /app/data && chown -R magcraw:magcraw /app

USER magcraw
ENV PORT=8900 \
    CI_BASE=https://cilisousuo.co \
    CI_RATE_LIMIT=60
EXPOSE 8900
VOLUME ["/app/data"]
ENTRYPOINT ["/app/magcraw"]