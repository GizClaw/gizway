# node:22.22.1-bookworm-slim
FROM node:22.22.1-bookworm-slim@sha256:4f77a690f2f8946ab16fe1e791a3ac0667ae1c3575c3e4d0d4589e9ed5bfaf3d AS build
WORKDIR /app
COPY web/apps/gizway/package.json web/apps/gizway/package-lock.json ./
RUN npm ci
COPY web/apps/gizway/ ./
ARG RELEASE_VERSION
ARG RELEASE_REVISION
ARG RELEASE_BUILD_TIME
RUN test -n "${RELEASE_VERSION}" \
    && test -n "${RELEASE_REVISION}" \
    && test -n "${RELEASE_BUILD_TIME}" \
    && printf 'export const releaseMetadata = Object.freeze({\n  version: "%s",\n  revision: "%s",\n  build_time: "%s",\n});\n' \
      "${RELEASE_VERSION}" "${RELEASE_REVISION}" "${RELEASE_BUILD_TIME}" > build/release-metadata.ts \
    && npm run build

# node:22.22.1-bookworm-slim
FROM node:22.22.1-bookworm-slim@sha256:4f77a690f2f8946ab16fe1e791a3ac0667ae1c3575c3e4d0d4589e9ed5bfaf3d
WORKDIR /app
ARG RELEASE_VERSION
ARG RELEASE_REVISION
ARG RELEASE_BUILD_TIME
LABEL org.opencontainers.image.source="https://github.com/idy/gizway" \
      org.opencontainers.image.version="${RELEASE_VERSION}" \
      org.opencontainers.image.revision="${RELEASE_REVISION}" \
      org.opencontainers.image.created="${RELEASE_BUILD_TIME}"
ENV HOST=0.0.0.0 PORT=3000 NODE_ENV=production
COPY --from=build --chown=node:node /app/dist/standalone/ ./dist/standalone/
# vinext beta's standalone tracer omits its React peer dependency closure.
COPY --from=build --chown=node:node /app/node_modules/react/ ./dist/standalone/node_modules/react/
COPY --from=build --chown=node:node /app/node_modules/react-dom/ ./dist/standalone/node_modules/react-dom/
COPY --from=build --chown=node:node /app/node_modules/react-server-dom-webpack/ ./dist/standalone/node_modules/react-server-dom-webpack/
COPY --from=build --chown=node:node /app/node_modules/scheduler/ ./dist/standalone/node_modules/scheduler/
USER node
EXPOSE 3000
CMD ["node", "dist/standalone/server.js"]
