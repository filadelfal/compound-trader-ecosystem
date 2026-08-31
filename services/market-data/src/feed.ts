import client from "prom-client";
import type { QuoteInput } from "./quotes";
import type { IngestResult, QuoteIngestor } from "./ingest";

const feedSuccess = new client.Counter({
  name: "market_data_feed_ingest_success_total",
  help: "Accepted or idempotent PAPER feed events.",
  labelNames: ["result"] as const,
});
const feedFailure = new client.Counter({
  name: "market_data_feed_ingest_failure_total",
  help: "Rejected or failed PAPER feed events.",
  labelNames: ["reason"] as const,
});
const feedDuplicate = new client.Counter({
  name: "market_data_feed_duplicate_total",
  help: "Idempotent duplicate PAPER feed events.",
});
const feedBackpressure = new client.Counter({
  name: "market_data_feed_backpressure_total",
  help: "PAPER feed events rejected because the bounded queue was full.",
});
const queueDepth = new client.Gauge({
  name: "market_data_feed_queue_depth",
  help: "Current bounded PAPER feed queue depth.",
});
const circuitOpen = new client.Gauge({
  name: "market_data_feed_circuit_open",
  help: "Whether the PAPER market-data provider circuit is open.",
});
const feedReady = new client.Gauge({
  name: "market_data_feed_ready",
  help: "Whether the PAPER market-data feed is fresh and healthy.",
});

export interface PaperQuoteProvider {
  readonly name: string;
  next(signal: AbortSignal): Promise<QuoteInput>;
  close?(): Promise<void>;
}

export interface FeedRunnerConfig {
  queueCapacity: number;
  requestTimeoutMs: number;
  maxRetries: number;
  backoffBaseMs: number;
  backoffMaxMs: number;
  circuitFailures: number;
  circuitCooldownMs: number;
  freshnessSeconds: number;
}

type FeedStatus = "disabled" | "starting" | "healthy" | "degraded" | "stopped";

export interface FeedSnapshot {
  status: FeedStatus;
  lastSuccessAt?: string;
  lastFailureAt?: string;
  lastFailureReason?: string;
  circuitOpen: boolean;
  queueDepth: number;
}

export class FeedHealth {
  private status: FeedStatus = "disabled";
  private lastSuccessAt?: Date;
  private lastFailureAt?: Date;
  private lastFailureReason?: string;
  private circuit = false;
  private depth = 0;

  starting(): void {
    this.status = "starting";
  }

  success(at: Date): void {
    this.status = "healthy";
    this.lastSuccessAt = new Date(at);
    this.lastFailureReason = undefined;
  }

  failure(reason: string, at: Date): void {
    this.status = "degraded";
    this.lastFailureAt = new Date(at);
    this.lastFailureReason = reason.slice(0, 64);
  }

  stopped(): void {
    this.status = "stopped";
  }

  setCircuitOpen(value: boolean): void {
    this.circuit = value;
    circuitOpen.set(value ? 1 : 0);
  }

  setQueueDepth(value: number): void {
    this.depth = Math.max(0, value);
    queueDepth.set(this.depth);
  }

  isFresh(now: Date, freshnessSeconds: number): boolean {
    const fresh =
      this.status === "healthy" &&
      !this.circuit &&
      this.lastSuccessAt !== undefined &&
      now.getTime() - this.lastSuccessAt.getTime() <= freshnessSeconds * 1000;
    feedReady.set(fresh ? 1 : 0);
    return fresh;
  }

  snapshot(): FeedSnapshot {
    return {
      status: this.status,
      ...(this.lastSuccessAt ? { lastSuccessAt: this.lastSuccessAt.toISOString() } : {}),
      ...(this.lastFailureAt ? { lastFailureAt: this.lastFailureAt.toISOString() } : {}),
      ...(this.lastFailureReason ? { lastFailureReason: this.lastFailureReason } : {}),
      circuitOpen: this.circuit,
      queueDepth: this.depth,
    };
  }
}

export class BoundedQuoteQueue {
  private readonly items: QuoteInput[] = [];

  constructor(private readonly capacity: number) {
    if (!Number.isInteger(capacity) || capacity < 1) {
      throw new Error("queue capacity must be a positive integer");
    }
  }

  tryPush(quote: QuoteInput): boolean {
    if (this.items.length >= this.capacity) return false;
    this.items.push(quote);
    return true;
  }

  shift(): QuoteInput | undefined {
    return this.items.shift();
  }

  get length(): number {
    return this.items.length;
  }
}

export class CircuitBreaker {
  private failures = 0;
  private openUntil = 0;

  constructor(
    private readonly failureThreshold: number,
    private readonly cooldownMs: number,
    private readonly now: () => number = () => Date.now(),
  ) {}

  canRequest(): boolean {
    if (this.openUntil === 0) return true;
    if (this.now() >= this.openUntil) {
      this.openUntil = 0;
      this.failures = 0;
      return true;
    }
    return false;
  }

  success(): void {
    this.failures = 0;
    this.openUntil = 0;
  }

  failure(): void {
    this.failures += 1;
    if (this.failures >= this.failureThreshold) {
      this.openUntil = this.now() + this.cooldownMs;
    }
  }

  get isOpen(): boolean {
    return this.openUntil > this.now();
  }
}

export function backoffDelay(
  attempt: number,
  baseMs: number,
  maxMs: number,
  jitter: () => number = Math.random,
): number {
  const exponential = Math.min(maxMs, baseMs * 2 ** Math.max(0, attempt));
  const boundedJitter = Math.max(0, Math.min(0.999999, jitter()));
  return Math.min(maxMs, exponential + Math.floor(boundedJitter * baseMs));
}

function abortError(): Error {
  const error = new Error("aborted");
  error.name = "AbortError";
  return error;
}

export function sleep(milliseconds: number, signal?: AbortSignal): Promise<void> {
  if (signal?.aborted) return Promise.reject(abortError());
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, milliseconds);
    const onAbort = () => {
      clearTimeout(timer);
      reject(abortError());
    };
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

export async function nextWithTimeout(
  provider: PaperQuoteProvider,
  timeoutMs: number,
  outerSignal: AbortSignal,
): Promise<QuoteInput> {
  if (outerSignal.aborted) throw abortError();

  const controller = new AbortController();
  const forwardAbort = () => controller.abort();
  outerSignal.addEventListener("abort", forwardAbort, { once: true });

  let timer: NodeJS.Timeout | undefined;
  try {
    const timeout = new Promise<never>((_resolve, reject) => {
      timer = setTimeout(() => {
        controller.abort();
        reject(new Error("provider_timeout"));
      }, timeoutMs);
    });
    return await Promise.race([provider.next(controller.signal), timeout]);
  } finally {
    if (timer) clearTimeout(timer);
    outerSignal.removeEventListener("abort", forwardAbort);
  }
}

const fixtureSymbols = ["EURUSD", "GBPUSD", "USDJPY"] as const;
const fixtureBases: Record<(typeof fixtureSymbols)[number], number> = {
  EURUSD: 1.1000,
  GBPUSD: 1.2800,
  USDJPY: 145.00,
};

export class DeterministicFixtureProvider implements PaperQuoteProvider {
  readonly name = "deterministic-fixture";
  private sequence = 0;

  constructor(
    private readonly intervalMs: number,
    private readonly now: () => Date = () => new Date(),
  ) {}

  async next(signal: AbortSignal): Promise<QuoteInput> {
    await sleep(this.intervalMs, signal);
    const symbol = fixtureSymbols[this.sequence % fixtureSymbols.length];
    const cycle = Math.floor(this.sequence / fixtureSymbols.length);
    const pip = symbol === "USDJPY" ? 0.01 : 0.0001;
    const bid = fixtureBases[symbol] + (cycle % 20) * pip;
    const ask = bid + 2 * pip;
    this.sequence += 1;
    return {
      provider: this.name,
      symbol,
      bid: Number(bid.toFixed(symbol === "USDJPY" ? 3 : 5)),
      ask: Number(ask.toFixed(symbol === "USDJPY" ? 3 : 5)),
      observedAt: this.now().toISOString(),
    };
  }
}

export class FeedRunner {
  private readonly abortController = new AbortController();
  private readonly queue: BoundedQuoteQueue;
  private readonly circuit: CircuitBreaker;
  private producer?: Promise<void>;
  private consumer?: Promise<void>;
  private started = false;

  constructor(
    private readonly provider: PaperQuoteProvider,
    private readonly ingestor: Pick<QuoteIngestor, "ingest">,
    private readonly health: FeedHealth,
    private readonly settings: FeedRunnerConfig,
    private readonly now: () => Date = () => new Date(),
    private readonly jitter: () => number = Math.random,
  ) {
    this.queue = new BoundedQuoteQueue(settings.queueCapacity);
    this.circuit = new CircuitBreaker(
      settings.circuitFailures,
      settings.circuitCooldownMs,
      () => this.now().getTime(),
    );
  }

  start(): void {
    if (this.started) return;
    this.started = true;
    this.health.starting();
    this.producer = this.produce();
    this.consumer = this.consume();
  }

  async stop(): Promise<void> {
    if (!this.started) return;
    this.abortController.abort();
    await Promise.allSettled([this.producer, this.consumer]);
    if (this.provider.close) {
      await this.provider.close();
    }
    this.health.setQueueDepth(0);
    this.health.stopped();
    this.started = false;
  }

  private async produce(): Promise<void> {
    const signal = this.abortController.signal;
    while (!signal.aborted) {
      if (!this.circuit.canRequest()) {
        this.health.setCircuitOpen(true);
        this.health.failure("provider_circuit_open", this.now());
        try {
          await sleep(Math.min(this.settings.circuitCooldownMs, 250), signal);
        } catch {
          return;
        }
        continue;
      }

      this.health.setCircuitOpen(false);
      let quote: QuoteInput | undefined;
      for (let attempt = 0; attempt <= this.settings.maxRetries && !signal.aborted; attempt += 1) {
        try {
          quote = await nextWithTimeout(this.provider, this.settings.requestTimeoutMs, signal);
          this.circuit.success();
          this.health.setCircuitOpen(false);
          break;
        } catch (error) {
          if (signal.aborted) return;
          this.circuit.failure();
          this.health.setCircuitOpen(this.circuit.isOpen);
          feedFailure.inc({ reason: error instanceof Error && error.message === "provider_timeout" ? "provider_timeout" : "provider_failure" });
          if (attempt >= this.settings.maxRetries || this.circuit.isOpen) {
            this.health.failure(this.circuit.isOpen ? "provider_circuit_open" : "provider_failure", this.now());
            break;
          }
          try {
            await sleep(
              backoffDelay(attempt, this.settings.backoffBaseMs, this.settings.backoffMaxMs, this.jitter),
              signal,
            );
          } catch {
            return;
          }
        }
      }

      if (!quote) {
        try {
          await sleep(this.settings.backoffBaseMs, signal);
        } catch {
          return;
        }
        continue;
      }

      if (!this.queue.tryPush(quote)) {
        feedBackpressure.inc();
        feedFailure.inc({ reason: "queue_full" });
        this.health.failure("queue_full", this.now());
      }
      this.health.setQueueDepth(this.queue.length);
    }
  }

  private async consume(): Promise<void> {
    const signal = this.abortController.signal;
    while (!signal.aborted) {
      const quote = this.queue.shift();
      this.health.setQueueDepth(this.queue.length);
      if (!quote) {
        try {
          await sleep(10, signal);
        } catch {
          return;
        }
        continue;
      }

      const result: IngestResult = await this.ingestor.ingest(quote);
      if (result.ok) {
        feedSuccess.inc({ result: result.status });
        if (result.status === "duplicate") feedDuplicate.inc();
        this.health.success(this.now());
      } else {
        feedFailure.inc({ reason: result.status });
        this.health.failure(result.status, this.now());
      }
    }
  }
}
