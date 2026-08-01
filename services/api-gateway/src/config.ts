import { z } from "zod";

const schema = z.object({
  NODE_ENV: z.enum(["development", "test", "production"]).default("development"),
  PORT: z.coerce.number().int().positive().default(3022),
  SERVICE_NAME: z.string().default("api-gateway"),
  LOG_LEVEL: z.string().default("info"),
  DATABASE_URL: z.string().default("postgresql://compound:compound_dev_password@postgres:5432/compound"),
  REDIS_URL: z.string().default("redis://redis:6379/0"),
  JWT_ACCESS_SECRET: z.string().min(32).default("development-access-secret-change-before-production"),
  JWT_ISSUER: z.string().min(1).default("compound-trader"),
  JWT_AUDIENCE: z.string().min(1).default("compound-trader-platform"),
  USER_MANAGEMENT_URL: z.string().url().default("http://user-management:3002"),
  UPSTREAM_TIMEOUT_MS: z.coerce.number().int().min(100).max(30_000).default(5_000),
  UPSTREAM_MAX_RESPONSE_BYTES: z.coerce.number().int().min(1_024).max(10_485_760).default(2_097_152)
}).superRefine((value, context) => {
  if (
    value.NODE_ENV === "production" &&
    value.JWT_ACCESS_SECRET === "development-access-secret-change-before-production"
  ) {
    context.addIssue({
      code: z.ZodIssueCode.custom,
      path: ["JWT_ACCESS_SECRET"],
      message: "JWT_ACCESS_SECRET must be explicitly configured in production"
    });
  }
});

export const config = schema.parse(process.env);
