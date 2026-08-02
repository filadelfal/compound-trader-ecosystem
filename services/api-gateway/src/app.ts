import express from "express";
import client from "prom-client";
import { authenticate, AuthOptions } from "./auth";
import { checkDatabase } from "./db";
import { checkCache } from "./cache";
import { config } from "./config";
import { proxyTo, UpstreamFetch } from "./proxy";

export interface AppOptions {
  auth: AuthOptions;
  userManagementUrl: string;
  tradingEngineUrl: string;
  marketDataUrl: string;
  timeoutMs: number;
  fetch?: UpstreamFetch;
  readiness?: () => Promise<void>;
}

client.collectDefaultMetrics({ prefix: `${config.SERVICE_NAME.replace(/-/g, "_")}_` });

export function createApp(options: AppOptions) {
  const app = express();
  app.disable("x-powered-by");
  app.use(express.json({ limit: "1mb" }));
  app.use(express.static("public", { extensions: ["html"], maxAge: options.fetch ? 0 : "1h" }));

  app.get("/health", (_req, res) => res.status(200).json({ status: "ok", service: config.SERVICE_NAME }));
  app.get("/ready", async (_req, res) => {
    try {
      await (options.readiness ?? (async () => Promise.all([checkDatabase(), checkCache()]).then(() => undefined)))();
      res.status(200).json({ ready: true, service: config.SERVICE_NAME });
    } catch (error) {
      res.status(503).json({ ready: false, service: config.SERVICE_NAME, error: error instanceof Error ? error.message : "unknown dependency error" });
    }
  });
  app.get("/metrics", async (_req, res) => {
    res.setHeader("Content-Type", client.register.contentType);
    res.end(await client.register.metrics());
  });
  app.get("/api/v1/ping", (_req, res) => res.status(200).json({ message: "pong", service: config.SERVICE_NAME }));

  const requireAccessToken = authenticate(options.auth);
  app.use("/api/v1/auth", proxyTo(options.userManagementUrl, options.timeoutMs, options.fetch, { injectUserId: false }));
  app.use("/api/v1/users", requireAccessToken, proxyTo(options.userManagementUrl, options.timeoutMs, options.fetch, { injectUserId: false, forwardAuthorization: true }));
  app.use("/api/v1/orders", requireAccessToken, proxyTo(options.tradingEngineUrl, options.timeoutMs, options.fetch));
  app.use("/api/v1/portfolio", requireAccessToken, proxyTo(options.tradingEngineUrl, options.timeoutMs, options.fetch));
  app.use("/api/v1/paper-account", requireAccessToken, proxyTo(options.tradingEngineUrl, options.timeoutMs, options.fetch));
  app.use("/api/v1/risk", requireAccessToken, proxyTo(options.tradingEngineUrl, options.timeoutMs, options.fetch));
  app.use("/api/v1/quotes", requireAccessToken, proxyTo(options.marketDataUrl, options.timeoutMs, options.fetch));

  app.use((_req, res) => res.status(404).json({ error: { code: "not_found", message: "Route not found" } }));
  app.use((error: unknown, _req: express.Request, res: express.Response, _next: express.NextFunction) => {
    res.status(502).json({ error: { code: "upstream_unavailable", message: error instanceof Error ? error.message : "Upstream service unavailable" } });
  });
  return app;
}

export const app = createApp({
  auth: { secret: config.JWT_ACCESS_SECRET, issuer: config.JWT_ISSUER, audience: config.JWT_AUDIENCE },
  userManagementUrl: config.USER_MANAGEMENT_URL,
  tradingEngineUrl: config.TRADING_ENGINE_URL,
  marketDataUrl: config.MARKET_DATA_URL,
  timeoutMs: config.UPSTREAM_TIMEOUT_MS,
});
