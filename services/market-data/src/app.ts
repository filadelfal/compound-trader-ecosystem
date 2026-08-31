import express from "express";
import client from "prom-client";
import { config } from "./config";
import { checkDatabase } from "./db";
import { checkCache, redis } from "./cache";
import { RedisQuoteStore, type QuoteStore } from "./quotes";
import { RedisCandleStore, timeframeSchema, type CandleStore } from "./candles";
import { QuoteIngestor } from "./ingest";
import type { FeedHealth } from "./feed";

client.collectDefaultMetrics({ prefix: `${config.SERVICE_NAME.replace(/-/g, "_")}_` });

export interface AppOptions {
  ingestor?: QuoteIngestor;
  feedHealth?: FeedHealth;
}

export function createApp(
  quoteStore: QuoteStore = new RedisQuoteStore(redis),
  now: () => Date = () => new Date(),
  candleStore: CandleStore = new RedisCandleStore(redis),
  options: AppOptions = {},
): express.Express {
  const app = express();
  const ingestor = options.ingestor ?? new QuoteIngestor(
    quoteStore,
    candleStore,
    {
      maxQuoteAgeSeconds: config.MAX_QUOTE_AGE_SECONDS,
      maxQuoteFutureSkewSeconds: config.MAX_QUOTE_FUTURE_SKEW_SECONDS,
      maxSpreadPips: config.MAX_SPREAD_PIPS,
    },
    now,
  );

  app.disable("x-powered-by");
  app.use(express.json({ limit: "1mb", strict: true }));

  app.get("/health", (_req, res) => {
    res.status(200).json({ status: "ok", service: config.SERVICE_NAME });
  });

  app.get("/ready", async (_req, res) => {
    try {
      await Promise.all([checkDatabase(), checkCache()]);
      if (config.MARKET_DATA_FEED_REQUIRED) {
        if (!options.feedHealth || !options.feedHealth.isFresh(now(), config.MARKET_DATA_FEED_FRESHNESS_SECONDS)) {
          res.status(503).json({
            ready: false,
            service: config.SERVICE_NAME,
            error: "market_feed_not_ready",
            feed: options.feedHealth?.snapshot() ?? { status: "unavailable" },
          });
          return;
        }
      }
      res.status(200).json({
        ready: true,
        service: config.SERVICE_NAME,
        ...(options.feedHealth ? { feed: options.feedHealth.snapshot() } : {}),
      });
    } catch {
      res.status(503).json({
        ready: false,
        service: config.SERVICE_NAME,
        error: "dependency_unavailable",
      });
    }
  });

  app.get("/metrics", async (_req, res) => {
    if (options.feedHealth) {
      options.feedHealth.isFresh(now(), config.MARKET_DATA_FEED_FRESHNESS_SECONDS);
    }
    res.setHeader("Content-Type", client.register.contentType);
    res.end(await client.register.metrics());
  });

  app.get("/api/v1/ping", (_req, res) => {
    res.status(200).json({ message: "pong", service: config.SERVICE_NAME });
  });

  app.post("/api/v1/quotes", async (req, res) => {
    const result = await ingestor.ingest(req.body);
    if (!result.ok) {
      res.status(result.httpStatus).json({
        error: result.status,
        ...(result.details ? { details: result.details } : {}),
        ...(result.spreadPips !== undefined ? { spreadPips: result.spreadPips } : {}),
      });
      return;
    }
    res.status(result.status === "accepted" ? 202 : 200).json({
      status: result.status,
      quote: result.quote,
      candleUpdates: result.candleUpdates,
    });
  });

  app.get("/api/v1/quotes/:symbol/latest", async (req, res) => {
    const symbol = req.params.symbol.toUpperCase();
    if (!/^(EURUSD|GBPUSD|USDJPY)$/.test(symbol)) {
      res.status(400).json({ error: "invalid_symbol" });
      return;
    }
    let quote;
    try {
      quote = await quoteStore.latest(symbol);
    } catch {
      res.status(503).json({ error: "quote_store_unavailable" });
      return;
    }
    if (!quote) {
      res.status(404).json({ error: "quote_not_found" });
      return;
    }
    res.status(200).json({ quote });
  });

  app.get("/api/v1/candles/:symbol/:timeframe", async (req, res) => {
    const symbol = req.params.symbol.toUpperCase();
    const timeframe = timeframeSchema.safeParse(req.params.timeframe.toUpperCase());
    if (!/^(EURUSD|GBPUSD|USDJPY)$/.test(symbol) || !timeframe.success) {
      res.status(400).json({ error: "invalid_candle_query" });
      return;
    }
    const limit = Math.min(Math.max(Number(req.query.limit) || 100, 1), 1000);
    try {
      const [current, closed, gaps] = await Promise.all([
        candleStore.current(symbol, timeframe.data),
        candleStore.closed(symbol, timeframe.data, limit),
        candleStore.gaps(symbol, timeframe.data, limit),
      ]);
      res.status(200).json({ symbol, timeframe: timeframe.data, current, closed, gaps });
    } catch {
      res.status(503).json({ error: "candle_store_unavailable" });
    }
  });

  app.use((_req, res) => {
    res.status(404).json({ error: "not_found" });
  });
  return app;
}

export const app = createApp();
