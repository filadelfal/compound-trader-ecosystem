import { z } from "zod";

const envBoolean = z.preprocess((value) => {
  if (typeof value !== "string") return value;
  const normalized = value.trim().toLowerCase();
  if (["1", "true", "yes", "on"].includes(normalized)) return true;
  if (["0", "false", "no", "off"].includes(normalized)) return false;
  return value;
}, z.boolean());

const schema = z.object({
  PORT: z.coerce.number().int().positive().default(3004),
  SERVICE_NAME: z.string().default("market-data"),
  LOG_LEVEL: z.string().default("info"),
  DATABASE_URL: z.string().default("postgresql://compound:compound_dev_password@postgres:5432/compound"),
  REDIS_URL: z.string().default("redis://redis:6379/0"),
  MAX_QUOTE_AGE_SECONDS: z.coerce.number().positive().default(30),
  MAX_QUOTE_FUTURE_SKEW_SECONDS: z.coerce.number().nonnegative().default(5),
  MAX_SPREAD_PIPS: z.coerce.number().positive().default(5),

  MARKET_DATA_FEED_MODE: z.enum(["disabled", "fixture"]).default("disabled"),
  MARKET_DATA_FEED_REQUIRED: envBoolean.default(false),
  MARKET_DATA_FEED_FRESHNESS_SECONDS: z.coerce.number().int().min(2).max(300).default(15),
  MARKET_DATA_FEED_INTERVAL_MS: z.coerce.number().int().min(50).max(60_000).default(1000),
  MARKET_DATA_FEED_QUEUE_CAPACITY: z.coerce.number().int().min(1).max(4096).default(64),
  MARKET_DATA_FEED_REQUEST_TIMEOUT_MS: z.coerce.number().int().min(50).max(60_000).default(2000),
  MARKET_DATA_FEED_MAX_RETRIES: z.coerce.number().int().min(0).max(10).default(3),
  MARKET_DATA_FEED_BACKOFF_BASE_MS: z.coerce.number().int().min(10).max(30_000).default(100),
  MARKET_DATA_FEED_BACKOFF_MAX_MS: z.coerce.number().int().min(10).max(120_000).default(5000),
  MARKET_DATA_FEED_CIRCUIT_FAILURES: z.coerce.number().int().min(1).max(100).default(5),
  MARKET_DATA_FEED_CIRCUIT_COOLDOWN_MS: z.coerce.number().int().min(100).max(300_000).default(10_000),
}).superRefine((value, context) => {
  if (value.MARKET_DATA_FEED_REQUIRED && value.MARKET_DATA_FEED_MODE === "disabled") {
    context.addIssue({
      code: z.ZodIssueCode.custom,
      path: ["MARKET_DATA_FEED_MODE"],
      message: "required market-data feed cannot be disabled",
    });
  }
  if (value.MARKET_DATA_FEED_BACKOFF_MAX_MS < value.MARKET_DATA_FEED_BACKOFF_BASE_MS) {
    context.addIssue({
      code: z.ZodIssueCode.custom,
      path: ["MARKET_DATA_FEED_BACKOFF_MAX_MS"],
      message: "backoff max must be greater than or equal to backoff base",
    });
  }
});

export const config = schema.parse(process.env);
