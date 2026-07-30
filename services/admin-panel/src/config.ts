import { z } from "zod";

const schema = z.object({
  PORT: z.coerce.number().int().positive().default(3020),
  SERVICE_NAME: z.string().default("admin-panel"),
  LOG_LEVEL: z.string().default("info"),
  DATABASE_URL: z.string().default("postgresql://compound:compound_dev_password@postgres:5432/compound"),
  REDIS_URL: z.string().default("redis://redis:6379/0")
});

export const config = schema.parse(process.env);
