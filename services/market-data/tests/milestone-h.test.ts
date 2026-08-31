import request from "supertest";
import { createApp } from "../src/app";
import { enrichQuote, InMemoryQuoteStore, quoteInputSchema, RedisQuoteStore } from "../src/quotes";
import { InMemoryCandleStore } from "../src/candles";

const clock = () => new Date("2026-08-31T10:00:01.000Z");
const quote = {
  provider: "test-provider",
  symbol: "EURUSD",
  bid: 1.101,
  ask: 1.1012,
  observedAt: "2026-08-31T10:00:00.000Z",
};

describe("Milestone H quote hardening", () => {
  it("rejects malformed, non-finite, and zero-spread quote values", () => {
    expect(quoteInputSchema.safeParse({ ...quote, bid: Number.NaN }).success).toBe(false);
    expect(quoteInputSchema.safeParse({ ...quote, ask: Number.POSITIVE_INFINITY }).success).toBe(false);
    expect(quoteInputSchema.safeParse({ ...quote, ask: quote.bid }).success).toBe(false);
    expect(quoteInputSchema.safeParse({ ...quote, observedAt: "not-a-timestamp" }).success).toBe(false);
  });

  it("recognizes a replay after a store instance restart", async () => {
    const values = new Map<string, string>();
    const redisLike = {
      get: async (key: string) => values.get(key) ?? null,
      set: async (key: string, value: string) => { values.set(key, value); return "OK"; },
    };
    const first = new RedisQuoteStore(redisLike);
    const stored = enrichQuote(quoteInputSchema.parse(quote), clock());
    expect(await first.put(stored)).toBe("accepted");

    const afterRestart = new RedisQuoteStore(redisLike);
    expect(await afterRestart.put(stored)).toBe("duplicate");
  });

  it("rejects unknown fields instead of silently stripping them", async () => {
    const app = createApp(new InMemoryQuoteStore(), clock, new InMemoryCandleStore());
    const response = await request(app).post("/api/v1/quotes").send({ ...quote, unexpected: true });
    expect(response.status).toBe(400);
    expect(response.body.error).toBe("invalid_quote");
  });

  it("rejects conflicting reuse of the same observation timestamp", async () => {
    const app = createApp(new InMemoryQuoteStore(), clock, new InMemoryCandleStore());
    expect((await request(app).post("/api/v1/quotes").send(quote)).status).toBe(202);
    const conflict = await request(app).post("/api/v1/quotes").send({ ...quote, bid: 1.1011, ask: 1.1013 });
    expect(conflict.status).toBe(409);
    expect(conflict.body.error).toBe("conflict");
  });

  it("keeps supported candle queries restricted to H1 and H4", async () => {
    const app = createApp(new InMemoryQuoteStore(), clock, new InMemoryCandleStore());
    expect((await request(app).get("/api/v1/candles/EURUSD/H1")).status).toBe(200);
    expect((await request(app).get("/api/v1/candles/EURUSD/H4")).status).toBe(200);
    expect((await request(app).get("/api/v1/candles/EURUSD/M15")).status).toBe(400);
    expect((await request(app).get("/api/v1/candles/AUDUSD/H1")).status).toBe(400);
  });
});
