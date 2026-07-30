import { NextFunction, Request, Response } from "express";

import { JwtService, JwtTokenError, jwtService } from "../jwt";
import { VerifiedAccessToken } from "../jwt/jwt.types";

declare global {
  namespace Express {
    interface Request {
      auth?: VerifiedAccessToken;
    }
  }
}

function readBearerToken(header: string | undefined): string {
  const [scheme, token, extra] = header?.trim().split(/\s+/) ?? [];
  if (scheme?.toLowerCase() !== "bearer" || !token || extra) {
    throw new JwtTokenError("TOKEN_INVALID", "Bearer access token is required");
  }
  return token;
}

export function authenticate(jwt: JwtService = jwtService) {
  return (request: Request, response: Response, next: NextFunction): void => {
    try {
      request.auth = jwt.verifyAccessToken(
        readBearerToken(request.get("authorization")),
      );
      next();
    } catch (error) {
      if (error instanceof JwtTokenError) {
        response.status(401).json({ error: { code: "invalid_token" } });
        return;
      }
      next(error);
    }
  };
}

export function requireRoles(...allowedRoles: string[]) {
  const allowed = new Set(allowedRoles);
  return (request: Request, response: Response, next: NextFunction): void => {
    if (!request.auth) {
      response.status(401).json({ error: { code: "authentication_required" } });
      return;
    }
    if (!request.auth.roles.some((role) => allowed.has(role))) {
      response.status(403).json({ error: { code: "forbidden" } });
      return;
    }
    next();
  };
}
