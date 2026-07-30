import { Router } from "express";
import { z } from "zod";

import { JwtService } from "../auth/jwt";
import { authenticate } from "../auth/authorization/authorization.middleware";
import { UserService } from "./user.service";

const updateSchema = z.object({
  displayName: z.string().trim().min(1).max(150).nullable().optional(),
  firstName: z.string().trim().min(1).max(100).nullable().optional(),
  lastName: z.string().trim().min(1).max(100).nullable().optional(),
  phoneNumber: z.string().trim().min(4).max(40).nullable().optional(),
  countryCode: z.string().length(2).transform((value) => value.toUpperCase()).nullable().optional(),
  timezone: z.string().trim().min(1).max(100).optional(),
  locale: z.string().trim().min(2).max(20).optional(),
  avatarUrl: z.string().url().max(2048).nullable().optional(),
  bio: z.string().max(2000).nullable().optional(),
}).strict().refine((value) => Object.keys(value).length > 0, {
  message: "At least one field is required",
});

export function createUserRouter(users: UserService, jwt?: JwtService): Router {
  const router = Router();
  router.use(authenticate(jwt));

  router.get("/me", async (request, response, next) => {
    try {
      const user = await users.getCurrentUser(request.auth!.subject);
      if (!user) {
        response.status(404).json({ error: { code: "user_not_found" } });
        return;
      }
      response.status(200).json({ data: user });
    } catch (error) {
      next(error);
    }
  });

  router.patch("/me", async (request, response, next) => {
    try {
      const input = updateSchema.parse(request.body);
      const user = await users.updateCurrentUser(request.auth!.subject, input);
      response.status(200).json({ data: user });
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
  return router;
}
