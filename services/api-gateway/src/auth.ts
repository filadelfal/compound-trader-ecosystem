import { NextFunction, Request, Response } from "express";
import jwt, { JwtPayload } from "jsonwebtoken";

export interface AuthenticatedRequest extends Request {
  auth?: { userId: string; sessionId: string; roles: string[] };
}

export interface AuthOptions {
  secret: string;
  issuer: string;
  audience: string;
}

export function authenticate(options: AuthOptions) {
  return (request: AuthenticatedRequest, response: Response, next: NextFunction): void => {
    const [scheme, token, extra] = request.get("authorization")?.trim().split(/\s+/) ?? [];
    if (scheme?.toLowerCase() !== "bearer" || !token || extra) {
      response.status(401).json({ error: { code: "authentication_required", message: "Bearer access token is required" } });
      return;
    }

    try {
      const decoded = jwt.verify(token, options.secret, {
        algorithms: ["HS256"],
        issuer: options.issuer,
        audience: options.audience,
      });
      if (typeof decoded !== "object" || decoded === null) throw new Error("invalid claims");
      const payload = decoded as JwtPayload;
      if (payload.token_use !== "access" || typeof payload.sub !== "string" ||
          typeof payload.session_id !== "string" ||
          !Array.isArray(payload.roles) || payload.roles.some((role) => typeof role !== "string")) {
        throw new Error("invalid claims");
      }
      request.auth = { userId: payload.sub, sessionId: payload.session_id, roles: [...new Set(payload.roles)] };
      next();
    } catch {
      response.status(401).json({ error: { code: "invalid_token", message: "Access token is invalid or expired" } });
    }
  };
}
