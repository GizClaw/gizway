FROM golang:1.26.5-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/gizpay ./cmd/gizpay \
 && CGO_ENABLED=0 go build -o /out/gizway ./cmd/gizway \
 && CGO_ENABLED=0 go build -o /out/fake-ai-provider ./cmd/fake-ai-provider \
 && CGO_ENABLED=0 go build -o /out/fake-payment-provider ./cmd/fake-payment-provider \
 && CGO_ENABLED=0 go build -o /out/fake-risk-provider ./cmd/fake-risk-provider \
 && CGO_ENABLED=0 go build -o /out/e2e-bootstrap ./tests/e2e/bootstrap

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ /usr/local/bin/
