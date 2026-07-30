import express from "express";
import helmet from "helmet";
import client from "prom-client";
import { config } from "./config";
import { checkDatabase } from "./db";
import { checkCache } from "./cache";
import { pool } from "./db";
import { emailSender } from "./auth/email/email.service";
import { passwordService } from "./auth/password/password.service";
import { createRegistrationRouter } from "./auth/registration/registration.routes";
import { RegistrationService } from "./auth/registration/registration.service";
import { createLoginRouter } from "./auth/login/login.routes";
import { LoginService } from "./auth/login/login.service";
import { PostgresSessionRepository } from "./auth/session/postgres-session.repository";
import { SessionService } from "./auth/session/session.service";
import { createSessionRouter } from "./auth/session/session.routes";
import { createPasswordResetRouter } from "./auth/password-reset/password-reset.routes";
import { PasswordResetService } from "./auth/password-reset/password-reset.service";
import { createUserRouter } from "./users/user.routes";
import { UserService } from "./users/user.service";
import { errorHandler, notFoundHandler } from "./http/error.middleware";
import { requestContext } from "./http/request-context.middleware";
import {
  authenticationRateLimit,
  loginRateLimit,
  recoveryRateLimit,
} from "./http/rate-limit.middleware";
import { openApiDocument } from "./openapi";

client.collectDefaultMetrics({ prefix: `${config.SERVICE_NAME.replace(/-/g, "_")}_` });

export const app = express();
app.disable("x-powered-by");
app.set("trust proxy", 1);
app.use(requestContext);
app.use(helmet());
app.use(express.json({ limit: "1mb" }));
app.use("/api/v1/auth", authenticationRateLimit);
app.use(
  "/api/v1/auth",
  createRegistrationRouter(
    new RegistrationService(pool, passwordService, emailSender),
  ),
);
app.use("/api/v1/users", createUserRouter(new UserService(pool)));
const sessionService = new SessionService(new PostgresSessionRepository(pool));
app.use(
  "/api/v1/auth",
  createSessionRouter({
    sessions: sessionService,
    resolveRoles: async (userId) => {
      const result = await pool.query<{ name: string }>(
        `SELECT roles.name
           FROM compound.roles AS roles
           JOIN compound.user_roles AS user_roles
             ON user_roles.role_id = roles.id
          WHERE user_roles.user_id = $1
          ORDER BY roles.name`,
        [userId],
      );
      return result.rows.map(({ name }) => name);
    },
  }),
);
app.use(
  "/api/v1/auth/login",
  loginRateLimit,
);
app.use(
  ["/api/v1/auth/forgot-password", "/api/v1/auth/reset-password"],
  recoveryRateLimit,
);
app.use(
  "/api/v1/auth",
  createLoginRouter(
    new LoginService(
      pool,
      passwordService,
      new SessionService(new PostgresSessionRepository(pool)),
    ),
  ),
);
app.use(
  "/api/v1/auth",
  createPasswordResetRouter(
    new PasswordResetService(pool, passwordService, emailSender),
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

app.get("/openapi.json", (_req, res) => {
  res.status(200).json(openApiDocument);
});

app.use(notFoundHandler);
app.use(errorHandler);
