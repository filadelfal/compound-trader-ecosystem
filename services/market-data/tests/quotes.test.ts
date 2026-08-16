import request from "supertest";
import { createApp } from "../src/app";
import { InMemoryQuoteStore } from "../src/quotes";

const clock = () => new Date("2026-08-01T08:00:01.000Z");

const quote = {
  provider: "test-provider",
  symbol: "eurusd",
  bid: 1.101,
  ask: 1.1012,
  observedAt: "2026-08-01T08:00:00.000Z",
};

describe("quote ingestion", () => {
  it("normalizes, accepts and retrieves a quote", async () => {
    const app = createApp(new InMemoryQuoteStore(), clock);
    const ingestion = await request(app).post("/api/v1/quotes").send(quote);
    expect(ingestion.status).toBe(202);
    expect(ingestion.body.quote.symbol).toBe("EURUSD");

    const latest = await request(app).get("/api/v1/quotes/EURUSD/latest");
    expect(latest.status).toBe(200);
    expect(latest.body.quote).toMatchObject({
      symbol: "EURUSD",
      bid: 1.101,
      ask: 1.1012,
      receivedAt: "2026-08-01T08:00:01.000Z",
    });
    expect(latest.body.quote.spreadPips).toBeCloseTo(2);
  });

  it("is idempotent for duplicate provider events", async () => {
    const app = createApp(new InMemoryQuoteStore(), clock);
    expect((await request(app).post("/api/v1/quotes").send(quote)).status).toBe(202);
    const duplicate = await request(app).post("/api/v1/quotes").send(quote);
    expect(duplicate.status).toBe(200);
    expect(duplicate.body.status).toBe("duplicate");
  });

  it("rejects crossed and out-of-order quotes", async () => {
    const app = createApp(new InMemoryQuoteStore(), clock);
    expect((await request(app).post("/api/v1/quotes").send({ ...quote, ask: 1 })).status).toBe(400);
    expect((await request(app).post("/api/v1/quotes").send(quote)).status).toBe(202);
    const stale = await request(app).post("/api/v1/quotes").send({ ...quote, observedAt: "2026-08-01T07:59:59.000Z" });
    expect(stale.status).toBe(409);
    expect(stale.body.error).toBe("out_of_order");
  });

  it("rejects unsupported symbols and stale or future quotes", async () => {
    const app = createApp(new InMemoryQuoteStore(), clock);
    expect((await request(app).post("/api/v1/quotes").send({ ...quote, symbol: "AUDUSD" })).status).toBe(400);
    expect((await request(app).post("/api/v1/quotes").send({ ...quote, observedAt: "2026-08-01T07:59:00.000Z" })).body.error).toBe("stale_quote");
    expect((await request(app).post("/api/v1/quotes").send({ ...quote, observedAt: "2026-08-01T08:00:10.000Z" })).body.error).toBe("future_quote");
  });

  it("rejects quotes whose spread exceeds the configured limit", async () => {
    const app = createApp(new InMemoryQuoteStore(), clock);
    const response = await request(app).post("/api/v1/quotes").send({ ...quote, ask: 1.103 });
    expect(response.status).toBe(422);
    expect(response.body.error).toBe("spread_too_wide");
  });

  it("fails closed when durable quote storage is unavailable", async () => {
    const unavailableStore = {
      put: async () => { throw new Error("redis unavailable"); },
      latest: async () => { throw new Error("redis unavailable"); },
    };
    const app = createApp(unavailableStore, clock);
    expect((await request(app).post("/api/v1/quotes").send(quote)).status).toBe(503);
    expect((await request(app).get("/api/v1/quotes/EURUSD/latest")).status).toBe(503);
  });
});
