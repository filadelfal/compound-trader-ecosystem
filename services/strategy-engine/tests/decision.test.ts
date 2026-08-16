import request from "supertest";
import { app } from "../src/app";
import { evaluateTradingDecision } from "../src/decision";

const now = new Date("2026-08-16T10:00:00.000Z");

function validRequest() {
  return {
    requestId: "630d91d8-aafa-4ba2-8d15-84fdfade243c",
    pair: "EURUSD",
    quote: {
      accepted: true,
      timestamp: "2026-08-16T09:59:50.000Z",
      spreadPips: 0.8
    },
    candles: {
      h1Closed: true,
      h4Closed: true,
      gapsDetected: false,
      h1Direction: "BUY",
      h4Direction: "BUY"
    },
    strategy: {
      id: "EMA_PULLBACK",
      conditionsMet: true,
      direction: "BUY",
      entry: 1.1,
      stopLoss: 1.095,
      takeProfit: 1.11
    },
    risk: {
      tradingEnabled: true,
      killSwitchActive: false,
      newsBlocked: false,
      sessionAllowed: true,
      riskPerTradePct: 0.5,
      dailyLossPct: 0.25,
      accountDrawdownPct: 1,
      openPositions: 0,
      existingPairExposure: false
    }
  };
}

describe("trading decision boundary", () => {
  it("returns a setup only when every contract and rule passes", () => {
    const decision = evaluateTradingDecision(validRequest(), now);

    expect(decision.outcome).toBe("TRADE_SETUP");
    if (decision.outcome === "TRADE_SETUP") {
      expect(decision.setup.riskReward).toBeCloseTo(2);
      expect(decision.setup.maximumRiskPct).toBe(0.5);
    }
  });

  it("fails closed when a mandatory risk gate is false", () => {
    const input = validRequest();
    input.risk.killSwitchActive = true;

    expect(evaluateTradingDecision(input, now)).toMatchObject({
      outcome: "NO_TRADE",
      reasons: ["INVALID_INPUT"]
    });
  });

  it("rejects stale quotes", () => {
    const input = validRequest();
    input.quote.timestamp = "2026-08-16T09:58:00.000Z";

    expect(evaluateTradingDecision(input, now)).toMatchObject({
      outcome: "NO_TRADE",
      reasons: ["STALE_QUOTE"]
    });
  });

  it("rejects timeframe conflict and poor reward-to-risk together", () => {
    const input = validRequest();
    input.candles.h4Direction = "SELL";
    input.strategy.takeProfit = 1.105;

    expect(evaluateTradingDecision(input, now)).toMatchObject({
      outcome: "NO_TRADE",
      reasons: ["TIMEFRAME_CONFLICT", "INSUFFICIENT_RISK_REWARD"]
    });
  });

  it("never throws or trades on malformed input", () => {
    expect(evaluateTradingDecision({ pair: "BTCUSD" }, now)).toMatchObject({
      outcome: "NO_TRADE",
      requestId: null,
      reasons: ["INVALID_INPUT"]
    });
  });

  it("exposes the decision endpoint with NO_TRADE as the invalid-input default", async () => {
    const response = await request(app)
      .post("/api/v1/decisions/evaluate")
      .send({ requestId: "not-a-uuid" });

    expect(response.status).toBe(200);
    expect(response.body).toMatchObject({
      outcome: "NO_TRADE",
      requestId: "not-a-uuid",
      reasons: ["INVALID_INPUT"]
    });
  });
});
