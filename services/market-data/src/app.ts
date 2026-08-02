import { timingSafeEqual } from "crypto";
import express from "express";
import client from "prom-client";
import { ZodError } from "zod";
import { checkCache } from "./cache";
import { config } from "./config";
import { checkDatabase } from "./db";
import { PostgresQuoteRepository } from "./postgres-quotes";
import { QuoteConflictError, QuoteRepository, QuoteService } from "./quotes";

client.collectDefaultMetrics({ prefix: `${config.SERVICE_NAME.replace(/-/g, "_")}_` });

export function createApp(
  repository: QuoteRepository = new PostgresQuoteRepository(),
  now: () => Date = () => new Date(),
  marketDataApiKey = config.MARKET_DATA_API_KEY,
  staleAfterSeconds = config.QUOTE_STALE_AFTER_SECONDS,
) {
  const app = express();
  const quotes = new QuoteService(repository, staleAfterSeconds, now);
  app.disable("x-powered-by");
  app.use(express.json({ limit: "256kb" }));

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
        error: error instanceof Error ? error.message : "unknown dependency error",
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

  app.post("/internal/v1/quotes", async (req, res, next) => {
    try {
      if (!validCredential(req.header("X-Market-Data-Key"), marketDataApiKey)) {
        res.status(401).json({ error: { code: "unauthorized", message: "valid market-data credentials are required" } });
        return;
      }
      const result = await quotes.ingest(req.body);
      res.status(result.created ? 201 : 200).json(result);
    } catch (error) {
      next(error);
    }
  });

  app.get("/api/v1/quotes/:symbol", async (req, res, next) => {
    try {
      const result = await quotes.latest([req.params.symbol]);
      if (!result[0]) {
        res.status(404).json({ error: { code: "quote_not_found", message: "no quote is available for symbol" } });
        return;
      }
      res.status(200).json(result[0]);
    } catch (error) {
      next(error);
    }
  });

  app.get("/api/v1/quotes", async (req, res, next) => {
    try {
      const requested = typeof req.query.symbols === "string"
        ? req.query.symbols.split(",").map((value) => value.trim()).filter(Boolean)
        : [];
      const result = await quotes.latest(requested);
      const found = new Set(result.map((quote) => quote.symbol));
      res.status(200).json({
        quotes: result,
        missingSymbols: [...new Set(requested.map((symbol) => symbol.toUpperCase()))].filter((symbol) => !found.has(symbol)),
      });
    } catch (error) {
      next(error);
    }
  });

  app.use((_req, res) => {
    res.status(404).json({ error: { code: "not_found", message: "route not found" } });
  });

  app.use((error: unknown, _req: express.Request, res: express.Response, _next: express.NextFunction) => {
    if (error instanceof QuoteConflictError) {
      res.status(409).json({ error: { code: "quote_conflict", message: error.message } });
      return;
    }
    if (error instanceof ZodError || error instanceof Error) {
      res.status(400).json({
        error: {
          code: "invalid_quote_request",
          message: error instanceof ZodError ? error.issues[0]?.message ?? "invalid request" : error.message,
        },
      });
      return;
    }
    res.status(500).json({ error: { code: "internal_error", message: "unexpected error" } });
  });

  return app;
}

function validCredential(provided: string | undefined, configured: string): boolean {
  if (!provided || !configured || provided.length !== configured.length) {
    return false;
  }
  return timingSafeEqual(Buffer.from(provided), Buffer.from(configured));
}

export const app = createApp();
