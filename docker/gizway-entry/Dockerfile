FROM traefik:v3.7.11@sha256:5203c3f39ca70de6790d964624e042463ffbd57715bc82be155cf224c0dd5144
ARG RELEASE_VERSION
ARG RELEASE_REVISION
ARG RELEASE_BUILD_TIME
LABEL org.opencontainers.image.source="https://github.com/GizClaw/gizway" \
      org.opencontainers.image.version="${RELEASE_VERSION}" \
      org.opencontainers.image.revision="${RELEASE_REVISION}" \
      org.opencontainers.image.created="${RELEASE_BUILD_TIME}" \
      org.opencontainers.image.base.name="traefik:v3.7.11@sha256:5203c3f39ca70de6790d964624e042463ffbd57715bc82be155cf224c0dd5144"
COPY docker/gizway-entry/traefik.yml /etc/traefik/traefik.yml
COPY docker/gizway-entry/dynamic/ /etc/gizway/
COPY docker/gizway-entry/entrypoint.sh /usr/local/bin/gizway-entry
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/gizway-entry"]
CMD []
