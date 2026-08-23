FROM ghcr.io/zitadel/zitadel:v4.17.1@sha256:3ac6910685d48f32481f01f45e3e6215efe5a9df2c069591b481e9a101712db5
ARG RELEASE_VERSION
ARG RELEASE_REVISION
ARG RELEASE_BUILD_TIME
LABEL org.opencontainers.image.source="https://github.com/GizClaw/gizway" \
      org.opencontainers.image.version="${RELEASE_VERSION}" \
      org.opencontainers.image.revision="${RELEASE_REVISION}" \
      org.opencontainers.image.created="${RELEASE_BUILD_TIME}" \
      org.opencontainers.image.base.name="ghcr.io/zitadel/zitadel:v4.17.1@sha256:3ac6910685d48f32481f01f45e3e6215efe5a9df2c069591b481e9a101712db5"
COPY docker/gizway-zitadel/config/zitadel.yaml /etc/gizway/zitadel.yaml
USER 1000:1000
