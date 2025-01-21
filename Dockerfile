FROM golang:1.23-alpine3.21 AS builder
RUN apk add --no-cache gcc musl-dev bash git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN bash scripts/install-all

FROM alpine:3.21
WORKDIR /app
COPY res ./res
COPY --from=builder /go/bin/bot .
ENTRYPOINT ["./bot"]
