FROM node:22-alpine AS builder

WORKDIR /app

COPY package.json package-lock.json tsconfig.json ./

RUN npm ci --no-audit --no-fund

COPY src ./src

RUN npm run build

# Remove development dependencies after compilation.
RUN npm prune --omit=dev --no-audit --no-fund


FROM node:22-alpine AS runtime

ENV NODE_ENV=production

WORKDIR /app

COPY --from=builder /app/package.json ./
COPY --from=builder /app/package-lock.json ./
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/dist ./dist

RUN chown -R node:node /app

USER node

EXPOSE 3002

CMD ["node", "dist/src/main.js"]

