import { randomUUID } from "node:crypto";

import { config } from "../../config";
import {
  JwtService,
  jwtService,
} from "../jwt";

import {
  RefreshTokenHasher,
  refreshTokenHasher,
} from "./refresh-token-hasher";

import {
  SessionSecurityError,
} from "./session.errors";

import type {
  CreateSessionInput,
  CreatedSession,
  RotatedSessionTokens,
  SessionRepository,
  StoredRefreshToken,
  UserSession,
} from "./session.types";

export interface SessionServiceOptions {
  sessionTtlDays: number;
  now?: () => Date;
  idGenerator?: () => string;
}

function addDays(
  date: Date,
  days: number,
): Date {
  const result = new Date(date);

  result.setUTCDate(
    result.getUTCDate() + days,
  );

  return result;
}

export class SessionService {
  private readonly repository: SessionRepository;
  private readonly jwt: JwtService;
  private readonly hasher: RefreshTokenHasher;
  private readonly sessionTtlDays: number;
  private readonly now: () => Date;
  private readonly idGenerator: () => string;

  public constructor(
    repository: SessionRepository,
    jwt: JwtService = jwtService,
    hasher: RefreshTokenHasher = refreshTokenHasher,
    options: SessionServiceOptions = {
      sessionTtlDays: config.JWT_REFRESH_EXPIRES_DAYS,
    },
  ) {
    this.repository = repository;
    this.jwt = jwt;
    this.hasher = hasher;
    this.sessionTtlDays = options.sessionTtlDays;
    this.now = options.now ?? (() => new Date());
    this.idGenerator =
      options.idGenerator ?? randomUUID;
  }

  public async createSession(
    input: CreateSessionInput,
  ): Promise<CreatedSession> {
    const now = this.now();
    const sessionId = this.idGenerator();
    const refreshTokenId = this.idGenerator();
    const expiresAt = addDays(
      now,
      this.sessionTtlDays,
    );

    const tokens = this.jwt.issueTokenPair(
      {
        userId: input.userId,
        sessionId,
        roles: input.roles ?? [],
      },
      refreshTokenId,
    );

    const session: UserSession = {
      id: sessionId,
      userId: input.userId,
      status: "active",
      metadata: input.metadata ?? {},
      createdAt: now,
      lastActivityAt: now,
      expiresAt,
    };

    const storedToken: StoredRefreshToken = {
      id: refreshTokenId,
      sessionId,
      userId: input.userId,
      tokenHash: this.hasher.hash(
        tokens.refreshToken,
      ),
      status: "active",
      createdAt: now,
      expiresAt,
    };

    await this.repository.createSession(
      session,
      storedToken,
    );

    return {
      session,
      accessToken: tokens.accessToken,
      refreshToken: tokens.refreshToken,
    };
  }

  public async rotateRefreshToken(
    refreshToken: string,
    roles: string[] = [],
  ): Promise<RotatedSessionTokens> {
    const claims =
      this.jwt.verifyRefreshToken(refreshToken);

    const now = this.now();
    const nextTokenId = this.idGenerator();

    const replacementTokens =
      this.jwt.issueTokenPair(
        {
          userId: claims.subject,
          sessionId: claims.session_id,
          roles,
        },
        nextTokenId,
      );

    const replacementToken: StoredRefreshToken = {
      id: nextTokenId,
      sessionId: claims.session_id,
      userId: claims.subject,
      tokenHash: this.hasher.hash(
        replacementTokens.refreshToken,
      ),
      status: "active",
      createdAt: now,
      expiresAt: addDays(
        now,
        this.sessionTtlDays,
      ),
    };

    const rotationResult =
      await this.repository
        .rotateRefreshTokenAtomically({
          sessionId: claims.session_id,
          userId: claims.subject,
          currentTokenId: claims.tokenId,
          currentTokenHash:
            this.hasher.hash(refreshToken),
          replacementToken,
          activityAt: now,
        });

    switch (rotationResult) {
      case "rotated":
        return {
          sessionId: claims.session_id,
          userId: claims.subject,
          accessToken:
            replacementTokens.accessToken,
          refreshToken:
            replacementTokens.refreshToken,
        };

      case "token_reused":
      case "token_mismatch":
        await this.repository.revokeSession(
          claims.session_id,
          "refresh_token_reuse_detected",
          now,
        );

        throw new SessionSecurityError(
          "REFRESH_TOKEN_REUSED",
          "Refresh token reuse was detected; the session has been revoked",
        );

      case "token_missing":
        throw new SessionSecurityError(
          "REFRESH_TOKEN_NOT_FOUND",
          "Refresh token record was not found",
        );

      case "session_missing":
        throw new SessionSecurityError(
          "SESSION_NOT_FOUND",
          "Session was not found",
        );

      case "session_expired":
        throw new SessionSecurityError(
          "SESSION_EXPIRED",
          "Session has expired",
        );

      case "session_revoked":
        throw new SessionSecurityError(
          "SESSION_REVOKED",
          "Session has been revoked",
        );
    }
  }

  public async logout(
    sessionId: string,
  ): Promise<boolean> {
    return this.repository.revokeSession(
      sessionId,
      "user_logout",
      this.now(),
    );
  }

  public async logoutAllDevices(
    userId: string,
  ): Promise<number> {
    return this.repository.revokeAllUserSessions(
      userId,
      "user_logout_all_devices",
      this.now(),
    );
  }

  public async listActiveSessions(
    userId: string,
  ): Promise<UserSession[]> {
    return this.repository.listActiveSessions(
      userId,
      this.now(),
    );
  }
}
