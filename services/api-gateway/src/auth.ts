import type { NextFunction, Request, Response } from "express";
import jwt, { type JwtPayload } from "jsonwebtoken";

import { config } from "./config";

export interface AuthContext {
  userId: string;
  sessionId: string;
  roles: string[];
}

declare global {
  namespace Express {
    interface Request {
      auth?: AuthContext;
    }
  }
}

function bearerToken(header: string | undefined): string | undefined {
  if (!header) return undefined;
  const [scheme, token, extra] = header.trim().split(/\s+/);
  return scheme?.toLowerCase() === "bearer" && token && !extra ? token : undefined;
}

function claims(payload: string | JwtPayload): AuthContext | undefined {
  if (typeof payload === "string") return undefined;
  if (payload.token_use !== "access" || typeof payload.sub !== "string") return undefined;
  if (typeof payload.session_id !== "string") return undefined;
  if (!Array.isArray(payload.roles) || payload.roles.some((role) => typeof role !== "string")) return undefined;
  return { userId: payload.sub, sessionId: payload.session_id, roles: [...new Set(payload.roles)] };
}

export function authenticate(request: Request, response: Response, next: NextFunction): void {
  const token = bearerToken(request.get("authorization"));
  if (!token) {
    response.status(401).json({ error: { code: "authentication_required" } });
    return;
  }

  try {
    const payload = jwt.verify(token, config.JWT_ACCESS_SECRET, {
      algorithms: ["HS256"],
      issuer: config.JWT_ISSUER,
      audience: config.JWT_AUDIENCE,
    });
    const auth = claims(payload);
    if (!auth) throw new Error("invalid claims");
    request.auth = auth;
    next();
  } catch {
    response.status(401).json({ error: { code: "invalid_token" } });
  }
}

export function requireRoles(...allowedRoles: string[]) {
  return (request: Request, response: Response, next: NextFunction): void => {
    if (!request.auth) {
      response.status(401).json({ error: { code: "authentication_required" } });
      return;
    }
    if (!allowedRoles.some((role) => request.auth?.roles.includes(role))) {
      response.status(403).json({ error: { code: "insufficient_permissions" } });
      return;
    }
    next();
  };
}
