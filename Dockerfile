FROM golang:1.27.1-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/trailnest-api ./cmd/api

FROM alpine:3.22

RUN apk add --no-cache ca-certificates wget \
    && addgroup -S app \
    && adduser -S -G app app

WORKDIR /app

COPY --from=builder /out/trailnest-api /app/trailnest-api

RUN mkdir -p /app/uploads/activities \
    && chown -R app:app /app/uploads

USER app

EXPOSE 8080

ENTRYPOINT ["/app/trailnest-api"]
