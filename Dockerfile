FROM golang:1.25 AS build
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGET_CMD
RUN test -n "$TARGET_CMD" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/app "$TARGET_CMD"
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/migrate ./cmd/migrate

FROM debian:bookworm-slim
WORKDIR /app

RUN apt-get update && apt-get install -y ca-certificates curl && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/app /app/app
COPY --from=build /out/migrate /app/migrate
COPY config.yaml /app/config.yaml
COPY migrations /app/migrations

CMD ["/app/app"]
