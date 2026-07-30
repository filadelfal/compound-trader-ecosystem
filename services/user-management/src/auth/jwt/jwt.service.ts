import jwt, {
  JsonWebTokenError,
  JwtPayload,
  NotBeforeError,
  SignOptions,
  TokenExpiredError,
} from "jsonwebtoken";

import { config } from "../../config";

import { JwtTokenError } from "./jwt.errors";

import type {
  AccessTokenClaims,
  AccessTokenInput,
  JwtServiceOptions,
  RefreshTokenClaims,
  RefreshTokenInput,
  TokenPair,
  VerifiedAccessToken,
  VerifiedRefreshToken,
} from "./jwt.types";

const JWT_ALGORITHM = "HS256" as const;

function isJwtPayload(
  value: string | JwtPayload,
): value is JwtPayload {
  return typeof value === "object" && value !== null;
}

function requireStringClaim(
  payload: JwtPayload,
  claimName: string,
): string {
  const value = payload[claimName];

  if (typeof value !== "string" || value.length === 0) {
    throw new JwtTokenError(
      "TOKEN_CLAIMS_INVALID",
      `JWT claim '${claimName}' is missing or invalid`,
    );
  }

  return value;
}

function requireNumberClaim(
  payload: JwtPayload,
  claimName: "iat" | "exp",
): number {
  const value = payload[claimName];

  if (typeof value !== "number") {
    throw new JwtTokenError(
      "TOKEN_CLAIMS_INVALID",
      `JWT claim '${claimName}' is missing or invalid`,
    );
  }

  return value;
}

function normalizeRoles(value: unknown): string[] {
  if (value === undefined) {
    return [];
  }

  if (
    !Array.isArray(value) ||
    value.some((role) => typeof role !== "string")
  ) {
    throw new JwtTokenError(
      "TOKEN_CLAIMS_INVALID",
      "JWT roles claim is invalid",
    );
  }

  return [...new Set(value)];
}

export function getJwtServiceOptions(): JwtServiceOptions {
  return {
    accessSecret: config.JWT_ACCESS_SECRET,
    refreshSecret: config.JWT_REFRESH_SECRET,
    issuer: config.JWT_ISSUER,
    audience: config.JWT_AUDIENCE,
    accessExpiresIn:
      config.JWT_ACCESS_EXPIRES_IN as SignOptions["expiresIn"],
    refreshExpiresIn:
      `${config.JWT_REFRESH_EXPIRES_DAYS}d` as SignOptions["expiresIn"],
  };
}

export class JwtService {
  private readonly options: JwtServiceOptions;

  public constructor(
    options: JwtServiceOptions = getJwtServiceOptions(),
  ) {
    this.options = options;
  }

  public signAccessToken(input: AccessTokenInput): string {
    const payload: AccessTokenClaims = {
      token_use: "access",
      session_id: input.sessionId,
      roles: [...new Set(input.roles ?? [])],
    };

    return jwt.sign(
      payload,
      this.options.accessSecret,
      {
        algorithm: JWT_ALGORITHM,
        issuer: this.options.issuer,
        audience: this.options.audience,
        subject: input.userId,
        expiresIn: this.options.accessExpiresIn,
      },
    );
  }

  public signRefreshToken(input: RefreshTokenInput): string {
    const payload: RefreshTokenClaims = {
      token_use: "refresh",
      session_id: input.sessionId,
    };

    return jwt.sign(
      payload,
      this.options.refreshSecret,
      {
        algorithm: JWT_ALGORITHM,
        issuer: this.options.issuer,
        audience: this.options.audience,
        subject: input.userId,
        jwtid: input.tokenId,
        expiresIn: this.options.refreshExpiresIn,
      },
    );
  }

  public issueTokenPair(
    accessInput: AccessTokenInput,
    refreshTokenId: string,
  ): TokenPair {
    return {
      accessToken: this.signAccessToken(accessInput),

      refreshToken: this.signRefreshToken({
        userId: accessInput.userId,
        sessionId: accessInput.sessionId,
        tokenId: refreshTokenId,
      }),

      accessTokenExpiresIn: this.options.accessExpiresIn,
      refreshTokenExpiresIn: this.options.refreshExpiresIn,
    };
  }

  public verifyAccessToken(token: string): VerifiedAccessToken {
    const payload = this.verify(
      token,
      this.options.accessSecret,
    );

    if (payload.token_use !== "access") {
      throw new JwtTokenError(
        "TOKEN_TYPE_INVALID",
        "Expected an access token",
      );
    }

    return {
      token_use: "access",
      subject: requireStringClaim(payload, "sub"),
      session_id: requireStringClaim(payload, "session_id"),
      roles: normalizeRoles(payload.roles),
      issuedAt: requireNumberClaim(payload, "iat"),
      expiresAt: requireNumberClaim(payload, "exp"),
      tokenId:
        typeof payload.jti === "string"
          ? payload.jti
          : undefined,
    };
  }

  public verifyRefreshToken(
    token: string,
  ): VerifiedRefreshToken {
    const payload = this.verify(
      token,
      this.options.refreshSecret,
    );

    if (payload.token_use !== "refresh") {
      throw new JwtTokenError(
        "TOKEN_TYPE_INVALID",
        "Expected a refresh token",
      );
    }

    return {
      token_use: "refresh",
      subject: requireStringClaim(payload, "sub"),
      session_id: requireStringClaim(payload, "session_id"),
      tokenId: requireStringClaim(payload, "jti"),
      issuedAt: requireNumberClaim(payload, "iat"),
      expiresAt: requireNumberClaim(payload, "exp"),
    };
  }

  public decode(token: string): JwtPayload | null {
    const decoded = jwt.decode(token);

    return decoded !== null &&
      typeof decoded === "object"
      ? decoded
      : null;
  }

  private verify(
    token: string,
    secret: string,
  ): JwtPayload {
    try {
      const payload = jwt.verify(
        token,
        secret,
        {
          algorithms: [JWT_ALGORITHM],
          issuer: this.options.issuer,
          audience: this.options.audience,
        },
      );

      if (!isJwtPayload(payload)) {
        throw new JwtTokenError(
          "TOKEN_CLAIMS_INVALID",
          "JWT payload is invalid",
        );
      }

      return payload;
    } catch (error) {
      if (error instanceof JwtTokenError) {
        throw error;
      }

      if (error instanceof TokenExpiredError) {
        throw new JwtTokenError(
          "TOKEN_EXPIRED",
          "JWT has expired",
          { cause: error },
        );
      }

      if (error instanceof NotBeforeError) {
        throw new JwtTokenError(
          "TOKEN_NOT_ACTIVE",
          "JWT is not active yet",
          { cause: error },
        );
      }

      if (error instanceof JsonWebTokenError) {
        throw new JwtTokenError(
          "TOKEN_INVALID",
          "JWT is invalid",
          { cause: error },
        );
      }

      throw new JwtTokenError(
        "TOKEN_INVALID",
        "JWT verification failed",
        {
          cause:
            error instanceof Error
              ? error
              : undefined,
        },
      );
    }
  }
}

export const jwtService = new JwtService();
