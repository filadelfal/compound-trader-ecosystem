import { Router } from "express";
import { z } from "zod";

import {
  PasswordPolicyError,
  PasswordResetService,
  PasswordResetTokenError,
} from "./password-reset.service";

const forgotPasswordSchema = z.object({
  email: z.string().trim().email().max(320),
}).strict();

const resetPasswordSchema = z.object({
  token: z.string().min(32).max(512),
  newPassword: z.string().min(12).max(256),
}).strict();

export function createPasswordResetRouter(service: PasswordResetService): Router {
  const router = Router();

  router.post("/forgot-password", async (request, response, next) => {
    try {
      const { email } = forgotPasswordSchema.parse(request.body);
      await service.requestReset(email, request.ip);
      response.status(202).json({
        data: {
          message: "If the account is eligible, a password-reset email will be sent.",
        },
      });
    } catch (error) {
      if (error instanceof z.ZodError) {
        response.status(400).json({
          error: { code: "validation_error", details: error.flatten() },
        });
        return;
      }
      next(error);
    }
  });

  router.post("/reset-password", async (request, response, next) => {
    try {
      const { token, newPassword } = resetPasswordSchema.parse(request.body);
      await service.resetPassword(token, newPassword, request.ip);
      response.status(200).json({ data: { passwordReset: true } });
    } catch (error) {
      if (error instanceof z.ZodError) {
        response.status(400).json({
          error: { code: "validation_error", details: error.flatten() },
        });
        return;
      }
      if (error instanceof PasswordPolicyError) {
        response.status(400).json({
          error: { code: "password_policy_failed", details: error.errors },
        });
        return;
      }
      if (error instanceof PasswordResetTokenError) {
        response.status(400).json({
          error: { code: "invalid_password_reset_token" },
        });
        return;
      }
      next(error);
    }
  });

  return router;
}

