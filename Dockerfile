FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /herald ./cmd/herald && \
    go build -trimpath -ldflags="-s -w" -o /heraldctl ./cmd/heraldctl

FROM alpine:3.22

RUN addgroup -S app && adduser -S app -G app
USER app
WORKDIR /app

COPY --from=builder /herald ./herald
COPY --from=builder /heraldctl ./heraldctl
COPY LICENSE NOTICE ./

EXPOSE 8080
ENTRYPOINT ["./herald"]
