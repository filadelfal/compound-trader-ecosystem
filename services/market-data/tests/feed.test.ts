import {
  BoundedQuoteQueue,
  CircuitBreaker,
  DeterministicFixtureProvider,
  FeedHealth,
  FeedRunner,
  backoffDelay,
  nextWithTimeout,
} from "../src/feed";
import { QuoteIngestor } from "../src/ingest";
import { InMemoryQuoteStore } from "../src/quotes";
import { InMemoryCandleStore } from "../src/candles";

describe("PAPER market-data feed", () => {
  it("emits deterministic quotes only for the supported symbols", async () => {
    let current = Date.parse("2026-08-31T10:00:00.000Z");
    const provider = new DeterministicFixtureProvider(1, () => new Date(current++));
    const controller = new AbortController();

    const quotes = [];
    for (let index = 0; index < 6; index += 1) {
      quotes.push(await provider.next(controller.signal));
    }

    expect(quotes.map((quote) => quote.symbol)).toEqual([
      "EURUSD", "GBPUSD", "USDJPY", "EURUSD", "GBPUSD", "USDJPY",
    ]);
    expect(quotes.every((quote) => quote.provider === "deterministic-fixture")).toBe(true);
    expect(quotes.every((quote) => Number.isFinite(quote.bid) && quote.ask > quote.bid)).toBe(true);
  });

  it("enforces bounded queue backpressure", () => {
    const queue = new BoundedQuoteQueue(1);
    const quote = {
      provider: "fixture",
      symbol: "EURUSD" as const,
      bid: 1.1,
      ask: 1.1002,
      observedAt: "2026-08-31T10:00:00.000Z",
    };
    expect(queue.tryPush(quote)).toBe(true);
    expect(queue.tryPush(quote)).toBe(false);
    expect(queue.length).toBe(1);
    expect(queue.shift()).toEqual(quote);
  });

  it("opens and recovers a deterministic circuit breaker", () => {
    let now = 1000;
    const circuit = new CircuitBreaker(2, 500, () => now);
    expect(circuit.canRequest()).toBe(true);
    circuit.failure();
    expect(circuit.isOpen).toBe(false);
    circuit.failure();
    expect(circuit.isOpen).toBe(true);
    expect(circuit.canRequest()).toBe(false);
    now = 1500;
    expect(circuit.canRequest()).toBe(true);
    circuit.success();
    expect(circuit.isOpen).toBe(false);
  });

  it("uses bounded exponential backoff with injectable jitter", () => {
    expect(backoffDelay(0, 100, 1000, () => 0)).toBe(100);
    expect(backoffDelay(1, 100, 1000, () => 0)).toBe(200);
    expect(backoffDelay(10, 100, 1000, () => 0.99)).toBe(1000);
  });

  it("times out an unresponsive provider", async () => {
    const provider = {
      name: "timeout-provider",
      next: (_signal: AbortSignal) => new Promise<never>(() => undefined),
    };
    const controller = new AbortController();
    await expect(nextWithTimeout(provider, 5, controller.signal)).rejects.toThrow("provider_timeout");
  });

  it("marks a fresh successful feed as ready and stale feed as not ready", () => {
    const health = new FeedHealth();
    health.starting();
    health.success(new Date("2026-08-31T10:00:00.000Z"));
    expect(health.isFresh(new Date("2026-08-31T10:00:05.000Z"), 10)).toBe(true);
    expect(health.isFresh(new Date("2026-08-31T10:00:11.000Z"), 10)).toBe(false);
  });

  it("starts and shuts down the deterministic fixture runner cleanly", async () => {
    const store = new InMemoryQuoteStore();
    const ingestor = new QuoteIngestor(
      store,
      new InMemoryCandleStore(),
      { maxQuoteAgeSeconds: 30, maxQuoteFutureSkewSeconds: 5, maxSpreadPips: 5 },
    );
    const health = new FeedHealth();
    const runner = new FeedRunner(
      new DeterministicFixtureProvider(1),
      ingestor,
      health,
      {
        queueCapacity: 4,
        requestTimeoutMs: 100,
        maxRetries: 1,
        backoffBaseMs: 1,
        backoffMaxMs: 4,
        circuitFailures: 2,
        circuitCooldownMs: 10,
        freshnessSeconds: 10,
      },
      () => new Date(),
      () => 0,
    );

    runner.start();
    await new Promise((resolve) => setTimeout(resolve, 30));
    await runner.stop();

    expect(health.snapshot().status).toBe("stopped");
    expect(health.snapshot().queueDepth).toBe(0);
  });
});
