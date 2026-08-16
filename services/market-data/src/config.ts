import { z } from "zod";

const schema = z.object({
  PORT: z.coerce.number().int().positive().default(3004),
  SERVICE_NAME: z.string().default("market-data"),
  LOG_LEVEL: z.string().default("info"),
  DATABASE_URL: z.string().default("postgresql://compound:compound_dev_password@postgres:5432/compound"),
  REDIS_URL: z.string().default("redis://redis:6379/0"),
  MAX_QUOTE_AGE_SECONDS: z.coerce.number().positive().default(30),
  MAX_QUOTE_FUTURE_SKEW_SECONDS: z.coerce.number().nonnegative().default(5),
  MAX_SPREAD_PIPS: z.coerce.number().positive().default(5)
});

export const config = schema.parse(process.env);
