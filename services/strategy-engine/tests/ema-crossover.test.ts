import request from "supertest";
import { app } from "../src/app";
import { evaluateEmaCrossover } from "../src/ema-crossover";

function candles(closes: number[], hours: number) {
  return closes.map((close, index) => ({
    close,
    closeTime: new Date(Date.UTC(2026, 7, 1, index * hours)).toISOString(),
    status: "CLOSED" as const
  }));
}

const rising = (count: number) => Array.from({ length: count }, (_, index) => 1.1 + index * 0.001);

describe("EMA crossover strategy", () => {
  it("confirms only a fresh H1 bullish crossover aligned with H4", () => {
    const h1 = [...Array.from({ length: 24 }, (_, index) => 1.124 - index * 0.001), 1.102, 1.16];
    const result = evaluateEmaCrossover({ pair: "EURUSD", h1: candles(h1, 1), h4: candles(rising(26), 4) });

    expect(result).toMatchObject({ strategy: "EMA_CROSSOVER", conditionsMet: true, direction: "BUY", reasons: [] });
    if (result.conditionsMet) expect(result.separationPips).toBeGreaterThanOrEqual(0.5);
  });

  it("does not repeat a signal when no fresh crossover occurred", () => {
    expect(evaluateEmaCrossover({ pair: "EURUSD", h1: candles(rising(26), 1), h4: candles(rising(26), 4) }))
      .toMatchObject({ conditionsMet: false, reasons: ["NO_FRESH_CROSSOVER"] });
  });

  it("fails closed on invalid or unordered candles", () => {
    expect(evaluateEmaCrossover({ pair: "EURUSD", h1: [], h4: [] }))
      .toMatchObject({ conditionsMet: false, reasons: ["INVALID_INPUT"] });

    const h1 = candles(rising(26), 1);
    h1[25].closeTime = h1[24].closeTime;
    expect(evaluateEmaCrossover({ pair: "EURUSD", h1, h4: candles(rising(26), 4) }))
      .toMatchObject({ conditionsMet: false, reasons: ["NON_CHRONOLOGICAL_CANDLES"] });
  });

  it("exposes a signal-only endpoint that defaults to no signal", async () => {
    const response = await request(app).post("/api/v1/strategies/ema-crossover/evaluate").send({});
    expect(response.status).toBe(200);
    expect(response.body).toMatchObject({
      strategy: "EMA_CROSSOVER",
      conditionsMet: false,
      direction: null,
      reasons: ["INVALID_INPUT"]
    });
  });
});
