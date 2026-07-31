FROM node:22-alpine AS builder

WORKDIR /app

COPY package.json package-lock.json tsconfig.json ./

RUN npm ci --no-audit --no-fund

COPY src ./src
COPY scripts ./scripts

RUN npm run build

# Remove development dependencies after compilation.
RUN npm prune --omit=dev --no-audit --no-fund


FROM node:22-alpine AS runtime

ENV NODE_ENV=production

WORKDIR /app

# Runtime requires Node.js only. Remove package managers and their bundled
# dependency trees to reduce the production image attack surface.
RUN rm -rf \
      /usr/local/lib/node_modules/npm \
      /usr/local/lib/node_modules/corepack \
    && rm -f \
      /usr/local/bin/npm \
      /usr/local/bin/npx \
      /usr/local/bin/corepack \
      /usr/local/bin/yarn \
      /usr/local/bin/yarnpkg \
      /usr/local/bin/pnpm \
      /usr/local/bin/pnpx

COPY --from=builder /app/package.json ./
COPY --from=builder /app/package-lock.json ./
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/dist ./dist
COPY migrations ./migrations

RUN chown -R node:node /app

USER node

EXPOSE 3002

CMD ["node", "dist/src/main.js"]

