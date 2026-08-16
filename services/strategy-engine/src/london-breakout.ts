import { z } from "zod";

const candleSchema = z.object({
  openTime: z.string().datetime({ offset: true }),
  closeTime: z.string().datetime({ offset: true }),
  open: z.number().positive(),
  high: z.number().positive(),
  low: z.number().positive(),
  close: z.number().positive(),
  status: z.literal("CLOSED")
}).strict().refine((candle) =>
  candle.high >= Math.max(candle.open, candle.close) &&
  candle.low <= Math.min(candle.open, candle.close) &&
  Date.parse(candle.closeTime) > Date.parse(candle.openTime),
  "Candle values are inconsistent"
);

export const londonBreakoutInputSchema = z.object({
  pair: z.enum(["EURUSD", "GBPUSD"]),
  h1: z.array(candleSchema).min(9),
  alreadySignaled: z.boolean()
}).strict();

export type LondonBreakoutReason =
  | "INVALID_INPUT"
  | "ALREADY_SIGNALED"
  | "NON_CHRONOLOGICAL_CANDLES"
  | "INCOMPLETE_ASIAN_RANGE"
  | "OUTSIDE_ENTRY_WINDOW"
  | "RANGE_OUTSIDE_LIMITS"
  | "NO_BREAKOUT_CONFIRMATION";

export type LondonBreakoutResult =
  | {
      strategy: "LONDON_BREAKOUT";
      conditionsMet: true;
      direction: "BUY" | "SELL";
      reasons: [];
      range: { high: number; low: number; sizePips: number };
    }
  | {
      strategy: "LONDON_BREAKOUT";
      conditionsMet: false;
      direction: null;
      reasons: LondonBreakoutReason[];
    };

const RANGE_START_HOUR_UTC = 0;
const RANGE_END_HOUR_UTC = 7;
const ENTRY_START_HOUR_UTC = 8;
const ENTRY_END_HOUR_UTC = 10;
const MIN_RANGE_PIPS = 10;
const MAX_RANGE_PIPS = 60;
const BREAKOUT_BUFFER_PIPS = 2;

function utcDateKey(value: string): string {
  return new Date(value).toISOString().slice(0, 10);
}

export function evaluateLondonBreakout(rawInput: unknown): LondonBreakoutResult {
  const parsed = londonBreakoutInputSchema.safeParse(rawInput);
  if (!parsed.success) {
    return { strategy: "LONDON_BREAKOUT", conditionsMet: false, direction: null, reasons: ["INVALID_INPUT"] };
  }
  if (parsed.data.alreadySignaled) {
    return { strategy: "LONDON_BREAKOUT", conditionsMet: false, direction: null, reasons: ["ALREADY_SIGNALED"] };
  }

  const h1 = parsed.data.h1;
  const chronological = h1.every((candle, index) =>
    index === 0 || Date.parse(candle.openTime) > Date.parse(h1[index - 1].openTime)
  );
  if (!chronological) {
    return {
      strategy: "LONDON_BREAKOUT",
      conditionsMet: false,
      direction: null,
      reasons: ["NON_CHRONOLOGICAL_CANDLES"]
    };
  }

  const latest = h1.at(-1)!;
  const sessionDate = utcDateKey(latest.openTime);
  const latestHour = new Date(latest.openTime).getUTCHours();
  if (latestHour < ENTRY_START_HOUR_UTC || latestHour > ENTRY_END_HOUR_UTC) {
    return { strategy: "LONDON_BREAKOUT", conditionsMet: false, direction: null, reasons: ["OUTSIDE_ENTRY_WINDOW"] };
  }

  const rangeCandles = h1.filter((candle) => {
    const hour = new Date(candle.openTime).getUTCHours();
    return utcDateKey(candle.openTime) === sessionDate && hour >= RANGE_START_HOUR_UTC && hour <= RANGE_END_HOUR_UTC;
  });
  const rangeHours = new Set(rangeCandles.map((candle) => new Date(candle.openTime).getUTCHours()));
  if (rangeCandles.length !== 8 || rangeHours.size !== 8) {
    return { strategy: "LONDON_BREAKOUT", conditionsMet: false, direction: null, reasons: ["INCOMPLETE_ASIAN_RANGE"] };
  }

  const rangeHigh = Math.max(...rangeCandles.map((candle) => candle.high));
  const rangeLow = Math.min(...rangeCandles.map((candle) => candle.low));
  const sizePips = (rangeHigh - rangeLow) / 0.0001;
  if (sizePips < MIN_RANGE_PIPS || sizePips > MAX_RANGE_PIPS) {
    return { strategy: "LONDON_BREAKOUT", conditionsMet: false, direction: null, reasons: ["RANGE_OUTSIDE_LIMITS"] };
  }

  const buffer = BREAKOUT_BUFFER_PIPS * 0.0001;
  const direction = latest.close > rangeHigh + buffer
    ? "BUY"
    : latest.close < rangeLow - buffer
      ? "SELL"
      : null;
  if (!direction) {
    return {
      strategy: "LONDON_BREAKOUT",
      conditionsMet: false,
      direction: null,
      reasons: ["NO_BREAKOUT_CONFIRMATION"]
    };
  }

  return {
    strategy: "LONDON_BREAKOUT",
    conditionsMet: true,
    direction,
    reasons: [],
    range: { high: rangeHigh, low: rangeLow, sizePips }
  };
}
