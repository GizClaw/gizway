FROM golang:1.26.6-bookworm@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/gizpay ./cmd/gizpay \
 && CGO_ENABLED=0 go build -o /out/gizway ./cmd/gizway \
 && CGO_ENABLED=0 go build -o /out/fake-ai-provider ./cmd/fake-ai-provider \
 && CGO_ENABLED=0 go build -o /out/fake-credit-spy ./cmd/fake-credit-spy \
 && CGO_ENABLED=0 go build -o /out/fake-oauth-spy ./cmd/fake-oauth-spy \
 && CGO_ENABLED=0 go build -o /out/e2e-resources ./tests/e2e/resources

FROM debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ /usr/local/bin/
