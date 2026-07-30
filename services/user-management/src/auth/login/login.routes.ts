import { Router } from "express";
import { z } from "zod";

import {
  AccountLockedError,
  AccountUnavailableError,
  InvalidCredentialsError,
  LoginService,
} from "./login.service";

const loginSchema = z.object({
  email: z.string().trim().email().max(320),
  password: z.string().min(1).max(256),
}).strict();

export function createLoginRouter(service: LoginService): Router {
  const router = Router();

  router.post("/login", async (request, response, next) => {
    try {
      const input = loginSchema.parse(request.body);
      const result = await service.login({
        ...input,
        metadata: {
          ipAddress: request.ip,
          userAgent: request.get("user-agent"),
        },
      });
      response.status(200).json({
        data: {
          accessToken: result.accessToken,
          refreshToken: result.refreshToken,
          tokenType: "Bearer",
        },
      });
    } catch (error) {
      if (error instanceof z.ZodError) {
        response.status(400).json({ error: { code: "validation_error", details: error.flatten() } });
        return;
      }
      if (error instanceof AccountLockedError) {
        response.status(423).json({ error: { code: "account_locked" } });
        return;
      }
      if (error instanceof InvalidCredentialsError || error instanceof AccountUnavailableError) {
        response.status(401).json({ error: { code: "invalid_credentials" } });
        return;
      }
      next(error);
    }
  });

  return router;
}
