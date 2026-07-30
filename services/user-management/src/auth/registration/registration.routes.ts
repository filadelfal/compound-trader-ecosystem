import { Router } from "express";
import { z } from "zod";

import {
  PasswordPolicyError,
  RegistrationConflictError,
  RegistrationService,
  VerificationTokenError,
} from "./registration.service";

const registrationSchema = z.object({
  email: z.string().trim().email().max(320),
  password: z.string().min(12).max(256),
  displayName: z.string().trim().min(1).max(150).optional(),
}).strict();

const verificationSchema = z.object({
  token: z.string().min(32).max(512),
}).strict();

export function createRegistrationRouter(service: RegistrationService): Router {
  const router = Router();

  router.post("/register", async (request, response, next) => {
    try {
      const input = registrationSchema.parse(request.body);
      const result = await service.register(input);
      response.status(201).json({
        data: {
          userId: result.userId,
          verificationRequired: true,
        },
      });
    } catch (error) {
      if (error instanceof z.ZodError) {
        response.status(400).json({ error: { code: "validation_error", details: error.flatten() } });
        return;
      }
      if (error instanceof PasswordPolicyError) {
        response.status(400).json({ error: { code: "password_policy_failed", details: error.errors } });
        return;
      }
      if (error instanceof RegistrationConflictError) {
        response.status(409).json({ error: { code: "email_already_registered" } });
        return;
      }
      next(error);
    }
  });

  router.post("/verify-email", async (request, response, next) => {
    try {
      const { token } = verificationSchema.parse(request.body);
      await service.verifyEmail(token);
      response.status(200).json({ data: { verified: true } });
    } catch (error) {
      if (error instanceof z.ZodError) {
        response.status(400).json({ error: { code: "validation_error", details: error.flatten() } });
        return;
      }
      if (error instanceof VerificationTokenError) {
        response.status(400).json({ error: { code: "invalid_verification_token" } });
        return;
      }
      next(error);
    }
  });

  return router;
}
