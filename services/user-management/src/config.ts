import { z } from "zod";

const secretSchema = z
  .string()
  .min(64, "JWT secrets must contain at least 64 characters");

const schema = z.object({
  NODE_ENV: z
    .enum(["development", "test", "production"])
    .default("development"),

  PORT: z.coerce
    .number()
    .int()
    .positive()
    .default(3002),

  SERVICE_NAME: z
    .string()
    .min(1)
    .default("user-management"),

  LOG_LEVEL: z
    .enum(["error", "warn", "info", "http", "verbose", "debug", "silly"])
    .default("info"),

  DATABASE_URL: z
    .string()
    .url()
    .default(
      "postgresql://compound:compound_dev_password@postgres:5432/compound"
    ),

  REDIS_URL: z
    .string()
    .url()
    .default("redis://redis:6379/0"),

  JWT_ACCESS_SECRET: secretSchema,
  JWT_REFRESH_SECRET: secretSchema,

  JWT_ISSUER: z
    .string()
    .default("compound-trader"),

  JWT_AUDIENCE: z
    .string()
    .default("compound-trader-platform"),

  JWT_ACCESS_EXPIRES_IN: z
    .string()
    .default("15m"),

  JWT_REFRESH_EXPIRES_DAYS: z.coerce
    .number()
    .int()
    .min(1)
    .max(90)
    .default(7),

  EMAIL_VERIFICATION_EXPIRES_HOURS: z.coerce
    .number()
    .int()
    .min(1)
    .max(72)
    .default(24),

  PASSWORD_RESET_EXPIRES_MINUTES: z.coerce
    .number()
    .int()
    .min(5)
    .max(120)
    .default(30),

  ARGON2_MEMORY_COST: z.coerce
    .number()
    .int()
    .min(19456)
    .default(65536),

  ARGON2_TIME_COST: z.coerce
    .number()
    .int()
    .min(2)
    .default(3),

  ARGON2_PARALLELISM: z.coerce
    .number()
    .int()
    .min(1)
    .default(1),

  ACCOUNT_LOCK_THRESHOLD: z.coerce
    .number()
    .int()
    .min(3)
    .max(20)
    .default(5),

  ACCOUNT_LOCK_MINUTES: z.coerce
    .number()
    .int()
    .min(1)
    .max(1440)
    .default(15)
});

const parsed = schema.safeParse(process.env);

if (!parsed.success) {
  console.error(
    "Invalid user-management configuration:",
    parsed.error.flatten().fieldErrors
  );

  throw new Error("Invalid user-management configuration");
}

export const config = parsed.data;
export type Config = typeof config;
