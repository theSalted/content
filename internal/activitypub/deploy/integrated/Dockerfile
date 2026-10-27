# Build context produced by package.sh: locked dependencies, source and static Go binary.
FROM node@sha256:a9f5f7c91a432850b2a8a7797adf5eadb6c733ceed61167806cee7ea7fbc29df AS dependencies
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts --no-audit --no-fund && npm cache clean --force
FROM dependencies AS source
COPY src ./src
RUN rm src/migrate.ts src/import-identity.ts
FROM dependencies AS runtime
COPY --from=source /app/src ./src
COPY content-delivery /app/content-delivery
COPY deploy/integrated /app/deploy/integrated
USER node
ENV HOST=0.0.0.0 PORT=3000 NODE_ENV=production FEDERATION_ENABLED=false
EXPOSE 3000
CMD ["node", "deploy/integrated/entrypoint.ts"]
