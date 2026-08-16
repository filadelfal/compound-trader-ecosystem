import { evaluateEmaPullback } from "../src/ema-pullback";
import { exponentialMovingAverage } from "../src/indicators";
import request from "supertest";
import { app } from "../src/app";

function candles(closes: number[], hours: number) {
  return closes.map((close, index) => ({
    close,
    closeTime: new Date(Date.UTC(2026, 7, 1, index * hours)).toISOString(),
    status: "CLOSED" as const
  }));
}

function rising(count: number, start = 1.1, step = 0.001): number[] {
  return Array.from({ length: count }, (_, index) => start + index * step);
}

describe("EMA indicator", () => {
  it("uses an SMA seed and deterministic EMA recurrence", () => {
    expect(exponentialMovingAverage([1, 2, 3, 4, 5], 3)).toEqual([2, 3, 4]);
  });

  it("refuses invalid periods and insufficient price history", () => {
    expect(() => exponentialMovingAverage([1, 2], 1)).toThrow();
    expect(exponentialMovingAverage([1, 2], 3)).toEqual([]);
  });
});

describe("EMA pullback strategy", () => {
  it("confirms a buy only after aligned H1/H4 trend and H1 pullback recovery", () => {
    const h1Closes = [...rising(28), 1.122, 1.135];
    const result = evaluateEmaPullback({
      pair: "EURUSD",
      h1: candles(h1Closes, 1),
      h4: candles(rising(30), 4)
    });

    expect(result).toEqual({
      strategy: "EMA_PULLBACK",
      conditionsMet: true,
      direction: "BUY",
      reasons: []
    });
  });

  it("returns no setup when the latest candle has no pullback confirmation", () => {
    const result = evaluateEmaPullback({
      pair: "EURUSD",
      h1: candles(rising(30), 1),
      h4: candles(rising(30), 4)
    });

    expect(result).toMatchObject({
      conditionsMet: false,
      direction: null,
      reasons: ["NO_PULLBACK_CONFIRMATION"]
    });
  });

  it("fails closed on open, insufficient, or unordered candles", () => {
    const insufficient = candles(rising(10), 1);
    expect(evaluateEmaPullback({ pair: "EURUSD", h1: insufficient, h4: insufficient }))
      .toMatchObject({ conditionsMet: false, reasons: ["INVALID_INPUT"] });

    const unordered = candles(rising(30), 1);
    unordered[29].closeTime = unordered[28].closeTime;
    expect(evaluateEmaPullback({ pair: "EURUSD", h1: unordered, h4: candles(rising(30), 4) }))
      .toMatchObject({ conditionsMet: false, reasons: ["NON_CHRONOLOGICAL_CANDLES"] });
  });

  it("exposes a signal-only endpoint that fails closed", async () => {
    const response = await request(app)
      .post("/api/v1/strategies/ema-pullback/evaluate")
      .send({ pair: "EURUSD", h1: [], h4: [] });

    expect(response.status).toBe(200);
    expect(response.body).toMatchObject({
      strategy: "EMA_PULLBACK",
      conditionsMet: false,
      direction: null,
      reasons: ["INVALID_INPUT"]
    });
  });
});
