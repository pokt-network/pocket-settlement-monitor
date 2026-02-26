FROM golang:1.25.7-alpine AS builder

RUN apk add --no-cache make git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN make build-release \
    VERSION=${VERSION} \
    COMMIT=${COMMIT} \
    BUILD_DATE=${BUILD_DATE}

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tini && \
    addgroup -S psm && adduser -S psm -G psm && \
    mkdir -p /home/psm/data && chown psm:psm /home/psm/data

COPY --from=builder /app/bin/pocket-settlement-monitor /usr/local/bin/pocket-settlement-monitor

USER psm

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["pocket-settlement-monitor", "monitor", "--config", "/etc/psm/config.yaml"]
