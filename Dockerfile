FROM golang:1.24.5-alpine AS gobuilder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
RUN apk add --no-cache bash git
COPY cmd/bot/ cmd/bot/
COPY internal/ internal/
COPY lib/ lib/
COPY scripts/ scripts/
ARG SIREN_BOT_VERSION=devel
ENV CGO_ENABLED=0
ENV GOFLAGS=-trimpath
ENV LDFLAGS="-s -w"
RUN ./scripts/build-bot "$SIREN_BOT_VERSION"

FROM alpine:3.22
WORKDIR /app
RUN apk add --no-cache ca-certificates
COPY res/ ./res/
COPY --from=gobuilder /app/cmd/bot/bot ./cmd/bot/bot
ENTRYPOINT ["./cmd/bot/bot"]
