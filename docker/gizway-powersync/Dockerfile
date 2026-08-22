FROM journeyapps/powersync-service:1.24.0@sha256:0fc9f65e693c07f1206007acddb87141402c09ef20589e29a0dfe20d57ce80b6
ARG RELEASE_VERSION
ARG RELEASE_REVISION
ARG RELEASE_BUILD_TIME
LABEL org.opencontainers.image.source="https://github.com/idy/gizway" \
      org.opencontainers.image.version="${RELEASE_VERSION}" \
      org.opencontainers.image.revision="${RELEASE_REVISION}" \
      org.opencontainers.image.created="${RELEASE_BUILD_TIME}" \
      org.opencontainers.image.base.name="journeyapps/powersync-service:1.24.0@sha256:0fc9f65e693c07f1206007acddb87141402c09ef20589e29a0dfe20d57ce80b6"
COPY docker/gizway-powersync/config/ /etc/gizway/powersync/
COPY --chmod=0555 docker/gizway-powersync/entrypoint.sh /usr/local/bin/gizway-powersync-entrypoint
ENTRYPOINT ["/usr/local/bin/gizway-powersync-entrypoint", "node", "service/lib/entry.js"]
CMD ["start"]
