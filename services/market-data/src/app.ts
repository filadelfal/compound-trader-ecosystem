import express from "express";
import client from "prom-client";
import { config } from "./config";
import { checkDatabase } from "./db";
import { checkCache, redis } from "./cache";
import { enrichQuote, quoteInputSchema, RedisQuoteStore, type QuoteStore } from "./quotes";
import { CandleAggregator, RedisCandleStore, timeframeSchema, type CandleStore, type CandleUpdate } from "./candles";

client.collectDefaultMetrics({ prefix: `${config.SERVICE_NAME.replace(/-/g, "_")}_` });

export function createApp(
  quoteStore: QuoteStore = new RedisQuoteStore(redis),
  now: () => Date = () => new Date(),
  candleStore: CandleStore = new RedisCandleStore(redis),
): express.Express {
const app = express();
const candleAggregator = new CandleAggregator(candleStore);
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

app.post("/api/v1/quotes", async (req, res) => {
  const parsed = quoteInputSchema.safeParse(req.body);
  if (!parsed.success) {
    res.status(400).json({ error: "invalid_quote", details: parsed.error.flatten() });
    return;
  }
  const receivedAt = now();
  const ageMilliseconds = receivedAt.getTime() - Date.parse(parsed.data.observedAt);
  if (ageMilliseconds > config.MAX_QUOTE_AGE_SECONDS * 1000) {
    res.status(422).json({ error: "stale_quote" });
    return;
  }
  if (ageMilliseconds < -config.MAX_QUOTE_FUTURE_SKEW_SECONDS * 1000) {
    res.status(422).json({ error: "future_quote" });
    return;
  }
  const quote = enrichQuote(parsed.data, receivedAt);
  if (quote.spreadPips > config.MAX_SPREAD_PIPS) {
    res.status(422).json({ error: "spread_too_wide", spreadPips: quote.spreadPips });
    return;
  }
  let result;
  try {
    result = await quoteStore.put(quote);
  } catch {
    res.status(503).json({ error: "quote_store_unavailable" });
    return;
  }
  if (result === "out_of_order") {
    res.status(409).json({ error: result });
    return;
  }
  let candleUpdates: CandleUpdate[] = [];
  if (result === "accepted" || result === "duplicate") {
    try {
      candleUpdates = await candleAggregator.process(quote);
    } catch {
      res.status(503).json({ error: "candle_store_unavailable" });
      return;
    }
  }
  res.status(result === "accepted" ? 202 : 200).json({ status: result, quote, candleUpdates });
});

app.get("/api/v1/quotes/:symbol/latest", async (req, res) => {
  const symbol = req.params.symbol.toUpperCase();
  if (!/^[A-Z]{6}$/.test(symbol)) {
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
