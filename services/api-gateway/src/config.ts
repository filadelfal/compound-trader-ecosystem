import { z } from "zod";

const schema = z.object({
  NODE_ENV: z.enum(["development", "test", "production"]).default("development"),
  PORT: z.coerce.number().int().positive().default(3022),
  SERVICE_NAME: z.string().default("api-gateway"),
  LOG_LEVEL: z.string().default("info"),
  DATABASE_URL: z.string().default("postgresql://compound:compound_dev_password@postgres:5432/compound"),
  REDIS_URL: z.string().default("redis://redis:6379/0"),
  JWT_ACCESS_SECRET: z.string().min(64),
  JWT_ISSUER: z.string().min(1).default("compound-trader"),
  JWT_AUDIENCE: z.string().min(1).default("compound-trader-platform"),
  OPERATOR_ASSERTION_SECRET: z.string().min(64),
  OPERATOR_ASSERTION_ISSUER: z.string().min(1).default("compound-api-gateway"),
  OPERATOR_ASSERTION_AUDIENCE: z.string().min(1).default("compound-trading-engine"),
  OPERATOR_ASSERTION_LIFETIME_SECONDS: z.coerce.number().int().min(5).max(60).default(30),
  TRADING_ENGINE_URL: z.string().url().default("http://trading-engine:3003")
});

const testSecret = "test-only-operator-security-secret-0123456789abcdef-0123456789abcdef";
export const config = schema.parse(process.env.NODE_ENV === "test" ? {
  JWT_ACCESS_SECRET: testSecret,
  OPERATOR_ASSERTION_SECRET: testSecret,
  ...process.env,
} : process.env);
