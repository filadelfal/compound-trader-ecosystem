FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/service .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates wget
COPY --from=builder /out/service /usr/local/bin/service
EXPOSE 3003
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD wget -qO- http://127.0.0.1:3003/health >/dev/null || exit 1
CMD ["service"]
