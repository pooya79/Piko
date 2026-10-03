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
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/buildx ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/buildx-worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/buildx-river-migrate ./cmd/river-migrate

FROM alpine:3.24.2
RUN addgroup -S buildx && adduser -S -G buildx buildx
WORKDIR /app
COPY --from=build /out/buildx /app/buildx
COPY --from=build /out/buildx-worker /app/buildx-worker
COPY --from=build /out/buildx-river-migrate /app/buildx-river-migrate
COPY --from=styles /src/static /app/static
USER buildx
EXPOSE 8080
ENTRYPOINT ["/app/buildx"]
