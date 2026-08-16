import { z } from "zod";

export const supportedPairs = ["EURUSD", "GBPUSD", "USDJPY"] as const;
export const strategyIds = [
  "EMA_PULLBACK",
  "EMA_CROSSOVER",
  "TREND_CONTINUATION",
  "LONDON_BREAKOUT"
] as const;

const directionSchema = z.enum(["BUY", "SELL"]);

export const decisionRequestSchema = z.object({
  requestId: z.string().uuid(),
  pair: z.enum(supportedPairs),
  quote: z.object({
    accepted: z.literal(true),
    timestamp: z.string().datetime({ offset: true }),
    spreadPips: z.number().positive().max(5)
  }),
  candles: z.object({
    h1Closed: z.literal(true),
    h4Closed: z.literal(true),
    gapsDetected: z.literal(false),
    h1Direction: directionSchema,
    h4Direction: directionSchema
  }),
  strategy: z.object({
    id: z.enum(strategyIds),
    conditionsMet: z.literal(true),
    direction: directionSchema,
    entry: z.number().positive(),
    stopLoss: z.number().positive(),
    takeProfit: z.number().positive()
  }),
  risk: z.object({
    tradingEnabled: z.literal(true),
    killSwitchActive: z.literal(false),
    newsBlocked: z.literal(false),
    sessionAllowed: z.literal(true),
    riskPerTradePct: z.number().positive().max(1),
    dailyLossPct: z.number().min(0).max(2),
    accountDrawdownPct: z.number().min(0).max(5),
    openPositions: z.number().int().min(0).max(2),
    existingPairExposure: z.literal(false)
  })
}).strict();

export type DecisionRequest = z.infer<typeof decisionRequestSchema>;

export type DecisionReason =
  | "INVALID_INPUT"
  | "STALE_QUOTE"
  | "TIMEFRAME_CONFLICT"
  | "INVALID_PRICE_STRUCTURE"
  | "INSUFFICIENT_RISK_REWARD";

export type TradingDecision =
  | {
      outcome: "NO_TRADE";
      requestId: string | null;
      reasons: DecisionReason[];
      evaluatedAt: string;
    }
  | {
      outcome: "TRADE_SETUP";
      requestId: string;
      reasons: [];
      evaluatedAt: string;
      setup: {
        pair: DecisionRequest["pair"];
        strategy: DecisionRequest["strategy"]["id"];
        direction: DecisionRequest["strategy"]["direction"];
        entry: number;
        stopLoss: number;
        takeProfit: number;
        riskReward: number;
        maximumRiskPct: number;
      };
    };

const MAX_QUOTE_AGE_MS = 30_000;
const MAX_FUTURE_SKEW_MS = 2_000;
const MIN_RISK_REWARD = 2;
const FLOAT_COMPARISON_EPSILON = 1e-9;

function priceStructure(input: DecisionRequest): { valid: boolean; riskReward: number } {
  const { direction, entry, stopLoss, takeProfit } = input.strategy;
  const risk = direction === "BUY" ? entry - stopLoss : stopLoss - entry;
  const reward = direction === "BUY" ? takeProfit - entry : entry - takeProfit;

  if (risk <= 0 || reward <= 0) {
    return { valid: false, riskReward: 0 };
  }

  return { valid: true, riskReward: reward / risk };
}

export function evaluateTradingDecision(
  rawInput: unknown,
  now: Date = new Date()
): TradingDecision {
  const evaluatedAt = now.toISOString();
  const parsed = decisionRequestSchema.safeParse(rawInput);

  if (!parsed.success) {
    const requestId =
      typeof rawInput === "object" && rawInput !== null && "requestId" in rawInput &&
      typeof rawInput.requestId === "string"
        ? rawInput.requestId
        : null;

    return { outcome: "NO_TRADE", requestId, reasons: ["INVALID_INPUT"], evaluatedAt };
  }

  const input = parsed.data;
  const reasons: DecisionReason[] = [];
  const quoteAgeMs = now.getTime() - new Date(input.quote.timestamp).getTime();

  if (quoteAgeMs > MAX_QUOTE_AGE_MS || quoteAgeMs < -MAX_FUTURE_SKEW_MS) {
    reasons.push("STALE_QUOTE");
  }

  if (
    input.candles.h1Direction !== input.strategy.direction ||
    input.candles.h4Direction !== input.strategy.direction
  ) {
    reasons.push("TIMEFRAME_CONFLICT");
  }

  const structure = priceStructure(input);
  if (!structure.valid) {
    reasons.push("INVALID_PRICE_STRUCTURE");
  } else if (structure.riskReward + FLOAT_COMPARISON_EPSILON < MIN_RISK_REWARD) {
    reasons.push("INSUFFICIENT_RISK_REWARD");
  }

  if (reasons.length > 0) {
    return { outcome: "NO_TRADE", requestId: input.requestId, reasons, evaluatedAt };
  }

  return {
    outcome: "TRADE_SETUP",
    requestId: input.requestId,
    reasons: [],
    evaluatedAt,
    setup: {
      pair: input.pair,
      strategy: input.strategy.id,
      direction: input.strategy.direction,
      entry: input.strategy.entry,
      stopLoss: input.strategy.stopLoss,
      takeProfit: input.strategy.takeProfit,
      riskReward: structure.riskReward,
      maximumRiskPct: input.risk.riskPerTradePct
    }
  };
}
