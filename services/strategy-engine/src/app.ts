import express from "express";
import client from "prom-client";
import { config } from "./config";
import { checkDatabase } from "./db";
import { checkCache } from "./cache";
import { evaluateTradingDecision } from "./decision";
import { evaluateEmaPullback } from "./ema-pullback";
import { evaluateEmaCrossover } from "./ema-crossover";
import { evaluateAdxTrendContinuation } from "./adx-trend-continuation";

client.collectDefaultMetrics({ prefix: `${config.SERVICE_NAME.replace(/-/g, "_")}_` });

export const app = express();
app.disable("x-powered-by");
app.use(express.json({ limit: "1mb" }));

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

app.post("/api/v1/decisions/evaluate", (req, res) => {
  const decision = evaluateTradingDecision(req.body);
  res.status(200).json(decision);
});

app.post("/api/v1/strategies/ema-pullback/evaluate", (req, res) => {
  res.status(200).json(evaluateEmaPullback(req.body));
});

app.post("/api/v1/strategies/ema-crossover/evaluate", (req, res) => {
  res.status(200).json(evaluateEmaCrossover(req.body));
});

app.post("/api/v1/strategies/trend-continuation/evaluate", (req, res) => {
  res.status(200).json(evaluateAdxTrendContinuation(req.body));
});

app.use((_req, res) => {
  res.status(404).json({ error: "not_found" });
});
