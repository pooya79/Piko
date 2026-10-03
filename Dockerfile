# syntax=docker/dockerfile:1
FROM node:24-alpine AS styles
WORKDIR /src
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY assets ./assets
COPY scripts ./scripts
COPY internal ./internal
COPY static ./static
RUN pnpm run css

FROM golang:1.27.1-alpine3.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/piko ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/piko-worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/piko-migrate ./cmd/migrate

FROM alpine:3.24.2
RUN addgroup -S piko && adduser -S -G piko piko && mkdir /data && chown piko:piko /data
ENV DATABASE_PATH=/data/piko.db
WORKDIR /app
COPY --from=build /out/piko /app/piko
COPY --from=build /out/piko-worker /app/piko-worker
COPY --from=build /out/piko-migrate /app/piko-migrate
COPY --from=styles /src/static /app/static
USER piko
EXPOSE 8080
ENTRYPOINT ["/app/piko"]
