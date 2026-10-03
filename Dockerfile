FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/telebeans ./cmd/telebeans

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata && mkdir -p /data
WORKDIR /app
COPY --from=build /out/telebeans /usr/local/bin/telebeans
COPY LICENSE ./LICENSE

ENV TELEBEANS_CONFIG=/app/config.json \
    TELEBEANS_DB=/data/telebeans.db

ENTRYPOINT ["telebeans"]
CMD ["run"]
