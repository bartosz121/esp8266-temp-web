FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o esp8266-web .

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app/

COPY --from=builder /app/esp8266-web .
COPY --from=builder /app/index.html .

EXPOSE 8080

CMD ["/app/esp8266-web"]
