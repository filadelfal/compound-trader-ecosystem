import { z } from "zod";

const schema = z.object({
  PORT: z.coerce.number().int().positive().default(3022),
  SERVICE_NAME: z.string().default("api-gateway"),
  LOG_LEVEL: z.string().default("info"),
  DATABASE_URL: z.string().default("postgresql://compound:compound_dev_password@postgres:5432/compound"),
  REDIS_URL: z.string().default("redis://redis:6379/0"),
  JWT_ACCESS_SECRET: z.string().min(64).default("local_access_secret_change_me_0123456789_abcdefghijklmnopqrstuvwxyz_ABCD"),
  JWT_ISSUER: z.string().default("compound-trader"),
  JWT_AUDIENCE: z.string().default("compound-trader-platform"),
  USER_MANAGEMENT_URL: z.string().url().default("http://user-management:3002"),
  TRADING_ENGINE_URL: z.string().url().default("http://trading-engine:3003"),
  MARKET_DATA_URL: z.string().url().default("http://market-data:3004"),
  UPSTREAM_TIMEOUT_MS: z.coerce.number().int().min(100).max(30000).default(5000)
});

export const config = schema.parse(process.env);
