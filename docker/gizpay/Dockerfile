# golang:1.26.6-bookworm
FROM golang:1.26.6-bookworm@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG RELEASE_VERSION
ARG RELEASE_REVISION
ARG RELEASE_BUILD_TIME
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
    -ldflags "-s -w -X github.com/idy/gizway/internal/buildinfo.Version=${RELEASE_VERSION} -X github.com/idy/gizway/internal/buildinfo.Revision=${RELEASE_REVISION} -X github.com/idy/gizway/internal/buildinfo.BuildTime=${RELEASE_BUILD_TIME}" \
    -o /out/gizpay ./cmd/gizpay

# debian:bookworm-slim
FROM debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241
ARG RELEASE_VERSION
ARG RELEASE_REVISION
ARG RELEASE_BUILD_TIME
LABEL org.opencontainers.image.source="https://github.com/idy/gizway" \
      org.opencontainers.image.version="${RELEASE_VERSION}" \
      org.opencontainers.image.revision="${RELEASE_REVISION}" \
      org.opencontainers.image.created="${RELEASE_BUILD_TIME}"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/gizpay /usr/local/bin/gizpay
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/gizpay"]
CMD ["serve"]
