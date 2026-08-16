import { z } from "zod";
import { strategyIds, supportedPairs } from "./decision";

const signalSchema = z.object({
  strategy: z.enum(strategyIds),
  conditionsMet: z.boolean(),
  direction: z.enum(["BUY", "SELL"]).nullable(),
  reasons: z.array(z.string().min(1)).max(20)
}).strict().superRefine((signal, context) => {
  if (signal.conditionsMet && (signal.direction === null || signal.reasons.length > 0)) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: "Met signals require a direction and no rejection reasons" });
  }
  if (!signal.conditionsMet && (signal.direction !== null || signal.reasons.length === 0)) {
    context.addIssue({ code: z.ZodIssueCode.custom, message: "Rejected signals require reasons and no direction" });
  }
});

export const strategyManagerInputSchema = z.object({
  requestId: z.string().uuid(),
  pair: z.enum(supportedPairs),
  signals: z.array(signalSchema).min(1).max(4)
}).strict();

type StrategyId = typeof strategyIds[number];
type Direction = "BUY" | "SELL";

export type StrategyManagerResult =
  | {
      outcome: "NO_TRADE";
      requestId: string | null;
      reason: "INVALID_INPUT" | "DUPLICATE_STRATEGY" | "NO_STRATEGY_SIGNAL" | "STRATEGY_CONFLICT";
      evaluations: number;
    }
  | {
      outcome: "SELECTED_SETUP";
      requestId: string;
      pair: typeof supportedPairs[number];
      direction: Direction;
      selectedStrategy: StrategyId;
      confirmingStrategies: StrategyId[];
      evaluations: number;
    };

const priority: Record<StrategyId, number> = {
  LONDON_BREAKOUT: 4,
  TREND_CONTINUATION: 3,
  EMA_PULLBACK: 2,
  EMA_CROSSOVER: 1
};

export function selectStrategySetup(rawInput: unknown): StrategyManagerResult {
  const parsed = strategyManagerInputSchema.safeParse(rawInput);
  if (!parsed.success) {
    const requestId = typeof rawInput === "object" && rawInput !== null && "requestId" in rawInput &&
      typeof rawInput.requestId === "string" ? rawInput.requestId : null;
    return { outcome: "NO_TRADE", requestId, reason: "INVALID_INPUT", evaluations: 0 };
  }

  const { requestId, pair, signals } = parsed.data;
  const strategies = signals.map((signal) => signal.strategy);
  if (new Set(strategies).size !== strategies.length) {
    return { outcome: "NO_TRADE", requestId, reason: "DUPLICATE_STRATEGY", evaluations: signals.length };
  }

  const candidates = signals.filter((signal): signal is typeof signal & { direction: Direction } =>
    signal.conditionsMet && signal.direction !== null
  );
  if (candidates.length === 0) {
    return { outcome: "NO_TRADE", requestId, reason: "NO_STRATEGY_SIGNAL", evaluations: signals.length };
  }

  if (new Set(candidates.map((candidate) => candidate.direction)).size > 1) {
    return { outcome: "NO_TRADE", requestId, reason: "STRATEGY_CONFLICT", evaluations: signals.length };
  }

  const ordered = [...candidates].sort((left, right) => priority[right.strategy] - priority[left.strategy]);
  const selected = ordered[0];
  return {
    outcome: "SELECTED_SETUP",
    requestId,
    pair,
    direction: selected.direction,
    selectedStrategy: selected.strategy,
    confirmingStrategies: ordered.slice(1).map((candidate) => candidate.strategy),
    evaluations: signals.length
  };
}
