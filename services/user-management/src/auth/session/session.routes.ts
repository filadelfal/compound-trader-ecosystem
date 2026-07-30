import { Router } from "express";
import { z } from "zod";

import { JwtService, JwtTokenError, jwtService } from "../jwt";
import { SessionSecurityError } from "./session.errors";
import { SessionService } from "./session.service";

const refreshTokenSchema = z.object({
  refreshToken: z.string().min(1).max(8192),
}).strict();

export type RoleResolver = (userId: string) => Promise<string[]>;

export interface SessionRouterDependencies {
  sessions: SessionService;
  resolveRoles: RoleResolver;
  jwt?: JwtService;
}

function bearerToken(header: string | undefined): string {
  if (!header) {
    throw new JwtTokenError("TOKEN_INVALID", "Authorization header is required");
  }

  const [scheme, token, extra] = header.trim().split(/\s+/);
  if (scheme?.toLowerCase() !== "bearer" || !token || extra) {
    throw new JwtTokenError("TOKEN_INVALID", "Bearer access token is required");
  }
  return token;
}

function tokenError(response: import("express").Response): void {
  response.status(401).json({ error: { code: "invalid_token" } });
}

export function createSessionRouter({
  sessions,
  resolveRoles,
  jwt = jwtService,
}: SessionRouterDependencies): Router {
  const router = Router();

  router.post("/refresh", async (request, response, next) => {
    try {
      const { refreshToken } = refreshTokenSchema.parse(request.body);
      const claims = jwt.verifyRefreshToken(refreshToken);
      const roles = await resolveRoles(claims.subject);
      const result = await sessions.rotateRefreshToken(refreshToken, roles);
      response.status(200).json({
        data: {
          accessToken: result.accessToken,
          refreshToken: result.refreshToken,
          tokenType: "Bearer",
        },
      });
    } catch (error) {
      if (error instanceof z.ZodError) {
        response.status(400).json({
          error: { code: "validation_error", details: error.flatten() },
        });
        return;
      }
      if (error instanceof JwtTokenError || error instanceof SessionSecurityError) {
        tokenError(response);
        return;
      }
      next(error);
    }
  });

  router.post("/logout", async (request, response, next) => {
    try {
      const { refreshToken } = refreshTokenSchema.parse(request.body);
      const claims = jwt.verifyRefreshToken(refreshToken);
      await sessions.logout(claims.session_id);
      response.status(204).send();
    } catch (error) {
      if (error instanceof z.ZodError) {
        response.status(400).json({
          error: { code: "validation_error", details: error.flatten() },
        });
        return;
      }
      if (error instanceof JwtTokenError || error instanceof SessionSecurityError) {
        tokenError(response);
        return;
      }
      next(error);
    }
  });

  router.post("/logout-all", async (request, response, next) => {
    try {
      const claims = jwt.verifyAccessToken(
        bearerToken(request.get("authorization")),
      );
      await sessions.logoutAllDevices(claims.subject);
      response.status(204).send();
    } catch (error) {
      if (error instanceof JwtTokenError || error instanceof SessionSecurityError) {
        tokenError(response);
        return;
      }
      next(error);
    }
  });

  return router;
}
