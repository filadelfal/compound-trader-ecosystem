FROM node:22-alpine AS builder
WORKDIR /app
COPY package*.json tsconfig.json ./
RUN npm install
COPY src ./src
RUN npm run build

FROM node:22-alpine AS runtime
WORKDIR /app
ENV NODE_ENV=production
COPY package*.json ./
RUN npm install --omit=dev && npm cache clean --force
COPY --from=builder --chown=node:node /app/dist/src ./dist
USER node
EXPOSE 3004
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD node -e "require('http').get('http://127.0.0.1:3004/ready',r=>process.exit(r.statusCode===200?0:1)).on('error',()=>process.exit(1))"
CMD ["node", "dist/main.js"]
