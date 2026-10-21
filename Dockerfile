# Builds both services from one Dockerfile:
#   docker build --target idp -t timecard-idp .
#   docker build --target app -t timecard-app .   (includes the built Vue UI)

# ---- web UI ----------------------------------------------------------------
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---- Go binaries -------------------------------------------------------------
FROM golang:1.26-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/idp ./cmd/idp \
 && go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/app

# ---- runtime base ------------------------------------------------------------
FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S timecard && adduser -S -G timecard -H timecard
WORKDIR /srv
USER timecard

FROM runtime AS idp
COPY --from=go /out/idp /usr/local/bin/idp
EXPOSE 8081
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD wget -qO- http://127.0.0.1:8081/healthz >/dev/null || exit 1
ENTRYPOINT ["idp"]

FROM runtime AS app
COPY --from=go /out/app /usr/local/bin/app
COPY --from=web /src/web/dist /srv/web/dist
ENV APP_WEB_DIST=/srv/web/dist
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["app"]
