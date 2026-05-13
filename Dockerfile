FROM golang:1.26.3-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o strm2link .

FROM alpine:latest

COPY --from=builder /app/strm2link /usr/local/bin/

USER nobody

ENTRYPOINT ["strm2link"]