import { createApp } from "./app";
import { config } from "./config";
import { logger } from "./logger";
import { connectCache, closeCache, redis } from "./cache";
import { closeDatabase } from "./db";
import { RedisQuoteStore } from "./quotes";
import { RedisCandleStore } from "./candles";
import { QuoteIngestor } from "./ingest";
import { DeterministicFixtureProvider, FeedHealth, FeedRunner } from "./feed";

async function start(): Promise<void> {
  await connectCache();

  const quoteStore = new RedisQuoteStore(redis);
  const candleStore = new RedisCandleStore(redis);
  const feedHealth = new FeedHealth();
  const ingestor = new QuoteIngestor(
    quoteStore,
    candleStore,
    {
      maxQuoteAgeSeconds: config.MAX_QUOTE_AGE_SECONDS,
      maxQuoteFutureSkewSeconds: config.MAX_QUOTE_FUTURE_SKEW_SECONDS,
      maxSpreadPips: config.MAX_SPREAD_PIPS,
    },
  );

  let feedRunner: FeedRunner | undefined;
  if (config.MARKET_DATA_FEED_MODE === "fixture") {
    feedRunner = new FeedRunner(
      new DeterministicFixtureProvider(config.MARKET_DATA_FEED_INTERVAL_MS),
      ingestor,
      feedHealth,
      {
        queueCapacity: config.MARKET_DATA_FEED_QUEUE_CAPACITY,
        requestTimeoutMs: config.MARKET_DATA_FEED_REQUEST_TIMEOUT_MS,
        maxRetries: config.MARKET_DATA_FEED_MAX_RETRIES,
        backoffBaseMs: config.MARKET_DATA_FEED_BACKOFF_BASE_MS,
        backoffMaxMs: config.MARKET_DATA_FEED_BACKOFF_MAX_MS,
        circuitFailures: config.MARKET_DATA_FEED_CIRCUIT_FAILURES,
        circuitCooldownMs: config.MARKET_DATA_FEED_CIRCUIT_COOLDOWN_MS,
        freshnessSeconds: config.MARKET_DATA_FEED_FRESHNESS_SECONDS,
      },
    );
    feedRunner.start();
  }

  const app = createApp(quoteStore, () => new Date(), candleStore, { ingestor, feedHealth });
  const server = app.listen(config.PORT, "0.0.0.0", () => {
    logger.info({
      event: "service_started",
      port: config.PORT,
      feedMode: config.MARKET_DATA_FEED_MODE,
      feedRequired: config.MARKET_DATA_FEED_REQUIRED,
    });
  });

  let shuttingDown = false;
  async function shutdown(signal: string): Promise<void> {
    if (shuttingDown) return;
    shuttingDown = true;
    logger.info({ event: "shutdown_started", signal });

    await feedRunner?.stop();

    server.close(async () => {
      await Promise.allSettled([closeCache(), closeDatabase()]);
      logger.info({ event: "shutdown_complete" });
      process.exit(0);
    });

    setTimeout(() => {
      logger.error({ event: "shutdown_timeout" });
      process.exit(1);
    }, 10_000).unref();
  }

  process.on("SIGTERM", () => void shutdown("SIGTERM"));
  process.on("SIGINT", () => void shutdown("SIGINT"));
}

start().catch((error) => {
  logger.error({ event: "startup_failed", error: error instanceof Error ? error.message : "startup error" });
  process.exit(1);
});
