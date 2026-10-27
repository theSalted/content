# Locked Node 24.19.0 OCI index; npm ci verifies the committed lockfile.
FROM node@sha256:a9f5f7c91a432850b2a8a7797adf5eadb6c733ceed61167806cee7ea7fbc29df AS dependencies
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts --no-audit --no-fund && npm cache clean --force

# Explicit one-shot operator image; never the default build or startup.
FROM dependencies AS migrator
COPY src/db.ts src/namespace.ts src/migrate.ts ./src/
COPY migrations ./migrations
USER node
ENV NODE_ENV=production
CMD ["node", "src/migrate.ts"]

# API and worker use this same image with distinct entrypoints, never a
# privileged migration URL. No tests, fixtures, operator CLI or migration SQL.
FROM dependencies AS runtime-source
COPY src ./src
RUN rm src/migrate.ts src/import-identity.ts

FROM dependencies AS runtime
COPY --from=runtime-source /app/src ./src
USER node
ENV HOST=0.0.0.0 PORT=3000 NODE_ENV=production FEDERATION_ENABLED=false
EXPOSE 3000
CMD ["node", "src/main.ts"]
