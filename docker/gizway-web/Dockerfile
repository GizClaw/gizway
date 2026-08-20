# node:22.22.1-bookworm-slim is used only to compile static assets.
FROM node:22.22.1-bookworm-slim@sha256:4f77a690f2f8946ab16fe1e791a3ac0667ae1c3575c3e4d0d4589e9ed5bfaf3d AS build
WORKDIR /app
COPY web/apps/gizway/package.json web/apps/gizway/package-lock.json ./
RUN npm ci
COPY web/apps/gizway/ ./
RUN npm run build

FROM caddy:2.11.4-alpine@sha256:5f5c8640aae01df9654968d946d8f1a56c497f1dd5c5cda4cf95ab7c14d58648
ARG RELEASE_VERSION
ARG RELEASE_REVISION
ARG RELEASE_BUILD_TIME
LABEL org.opencontainers.image.source="https://github.com/idy/gizway" \
      org.opencontainers.image.version="${RELEASE_VERSION}" \
      org.opencontainers.image.revision="${RELEASE_REVISION}" \
      org.opencontainers.image.created="${RELEASE_BUILD_TIME}"
COPY docker/gizway-web/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/dist/ /srv/
USER 65532:65532
EXPOSE 8080
