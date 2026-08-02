import { z } from "zod";

const schema = z.object({
  PORT: z.coerce.number().int().positive().default(3004),
  SERVICE_NAME: z.string().default("market-data"),
  LOG_LEVEL: z.string().default("info"),
  DATABASE_URL: z.string().default("postgresql://compound:compound_dev_password@postgres:5432/compound"),
  REDIS_URL: z.string().default("redis://redis:6379/0"),
  MARKET_DATA_API_KEY: z.string().default(""),
  QUOTE_STALE_AFTER_SECONDS: z.coerce.number().int().positive().default(60)
});

export const config = schema.parse(process.env);
