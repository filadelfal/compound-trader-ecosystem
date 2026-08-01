import express from "express";
import client from "prom-client";
import { config } from "./config";
import { checkDatabase } from "./db";
import { checkCache, redis } from "./cache";
import { authenticate, requireRoles } from "./auth";
import { createServiceProxy } from "./proxy";
import { observeRequests } from "./request-observability";
import { createRateLimit, RedisRateLimitStore } from "./rate-limit";

client.collectDefaultMetrics({ prefix: `${config.SERVICE_NAME.replace(/-/g, "_")}_` });

export const app = express();
app.disable("x-powered-by");
app.set("trust proxy", 1);
app.use(observeRequests);
app.use(express.json({ limit: "1mb" }));
app.use((_req, res, next) => {
  res.setHeader("X-Content-Type-Options", "nosniff");
  res.setHeader("X-Frame-Options", "DENY");
  res.setHeader("Referrer-Policy", "no-referrer");
  res.setHeader("Cache-Control", "no-store");
  next();
});

app.get("/health", (_req, res) => {
  res.status(200).json({ status: "ok", service: config.SERVICE_NAME });
});

app.get("/ready", async (_req, res) => {
  try {
    await Promise.all([checkDatabase(), checkCache()]);
    res.status(200).json({ ready: true, service: config.SERVICE_NAME });
  } catch (error) {
    res.status(503).json({
      ready: false,
      service: config.SERVICE_NAME,
      error: error instanceof Error ? error.message : "unknown dependency error"
    });
  }
});

app.get("/metrics", async (_req, res) => {
  res.setHeader("Content-Type", client.register.contentType);
  res.end(await client.register.metrics());
});

app.get("/api/v1/ping", (_req, res) => {
  res.status(200).json({ message: "pong", service: config.SERVICE_NAME });
});

const userManagementProxy = createServiceProxy(config.USER_MANAGEMENT_URL);
const authRateLimit = createRateLimit(new RedisRateLimitStore(redis), {
  prefix: "gateway:rate-limit:auth",
  max: config.AUTH_RATE_LIMIT_MAX,
  windowSeconds: config.AUTH_RATE_LIMIT_WINDOW_SECONDS,
});
app.use("/api/v1/auth", ...(config.NODE_ENV === "test" ? [] : [authRateLimit]), userManagementProxy);
app.use("/api/v1/users", authenticate, userManagementProxy);

app.get("/api/v1/me", authenticate, (req, res) => {
  res.status(200).json({ data: req.auth });
});

app.get("/api/v1/admin/ping", authenticate, requireRoles("admin"), (_req, res) => {
  res.status(200).json({ message: "pong", service: config.SERVICE_NAME, scope: "admin" });
});

app.use((_req, res) => {
  res.status(404).json({ error: "not_found" });
});

app.use((error: unknown, _req: express.Request, res: express.Response, _next: express.NextFunction) => {
  res.status(502).json({
    error: {
      code: "upstream_unavailable",
      message: error instanceof Error ? "The requested service is unavailable" : "Upstream request failed"
    }
  });
});
