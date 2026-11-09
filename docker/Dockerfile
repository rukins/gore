# The dashboard and the server are built here rather than on the deploy
# host, so a deployment needs nothing but Docker. Keeping the Go and Node
# toolchains in step with go.mod and dashboard/package.json on every host
# that ships this is otherwise a standing source of drift.

FROM node:22-alpine AS dashboard
WORKDIR /src/dashboard
COPY dashboard/package.json dashboard/package-lock.json ./
RUN npm ci
COPY dashboard/ ./
RUN npm run build

FROM golang:1.27-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The server embeds the dashboard, so it has to be in place before the
# build. See the go:embed directives in main.go.
COPY --from=dashboard /src/dashboard/build ./dashboard/build
ARG VERSION=dev
RUN echo "$VERSION" > internal/version/.version && \
    CGO_ENABLED=0 go build -o /gore

FROM alpine:3.22
# Signing in calls the auth service over TLS, which needs a trust store.
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=server /gore ./gore
# The deployment config is mounted at /app/data/goreconf.yml rather than
# copied in: it holds credentials, and baking it in would put them in
# every layer of a shared image. The same directory holds the SQLite
# file. See docker-compose.yml.
EXPOSE 9150
CMD ["/app/gore", "-s"]
