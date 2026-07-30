import express from "express";
import client from "prom-client";
import { config } from "./config";
import { checkDatabase } from "./db";
import { checkCache } from "./cache";
import { pool } from "./db";
import { emailSender } from "./auth/email/email.service";
import { passwordService } from "./auth/password/password.service";
import { createRegistrationRouter } from "./auth/registration/registration.routes";
import { RegistrationService } from "./auth/registration/registration.service";

client.collectDefaultMetrics({ prefix: `${config.SERVICE_NAME.replace(/-/g, "_")}_` });

export const app = express();
app.disable("x-powered-by");
app.use(express.json({ limit: "1mb" }));
app.use(
  "/api/v1/auth",
  createRegistrationRouter(
    new RegistrationService(pool, passwordService, emailSender),
  ),
);

app.get("/health", (_req, res) => {
  res.status(200).json({ status: "ok", service: config.SERVICE_NAME });
});

app.get("/ready", async (_req, res) => {
  try {
    await Promise.all([checkDatabase(), checkCache()]);
    res.status(200).json({ ready: true, service: config.SERVICE_NAME });
  } catch (error) {
    res.status(503).json({
      ready: false,
      service: config.SERVICE_NAME,
      error: error instanceof Error ? error.message : "unknown dependency error"
    });
  }
});

app.get("/metrics", async (_req, res) => {
  res.setHeader("Content-Type", client.register.contentType);
  res.end(await client.register.metrics());
});

app.get("/api/v1/ping", (_req, res) => {
  res.status(200).json({ message: "pong", service: config.SERVICE_NAME });
});

app.use((_req, res) => {
  res.status(404).json({ error: { code: "not_found" } });
});

app.use((
  error: unknown,
  _req: express.Request,
  res: express.Response,
  _next: express.NextFunction,
) => {
  res.status(500).json({ error: { code: "internal_error" } });
});
