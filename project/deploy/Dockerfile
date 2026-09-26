# Многоэтапная сборка, один бинарь. Зависимости не вендорятся — сборка тянет
# их с proxy.golang.org (на удалённом сервере сеть на сборке есть; отступление
# от NFR-D-03, см. decisions.md D-12).
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/portal ./cmd/portal

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 portal
COPY --from=build /out/portal /usr/local/bin/portal
USER portal
ENTRYPOINT ["/usr/local/bin/portal"]
