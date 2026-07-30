import {
  JwtService,
} from "../../src/auth/jwt";

import {
  RefreshTokenHasher,
  SessionSecurityError,
  SessionService,
} from "../../src/auth/session";

import type {
  AtomicRotationInput,
  RotationResult,
  SessionRepository,
  StoredRefreshToken,
  UserSession,
} from "../../src/auth/session";

const ACCESS_SECRET =
  "auth003-access-secret-0123456789abcdef-auth003-access-secret-0123456789abcdef";

const REFRESH_SECRET =
  "auth003-refresh-secret-0123456789abcdef-auth003-refresh-secret-0123456789abcdef";

class InMemorySessionRepository
  implements SessionRepository {
  public readonly sessions =
    new Map<string, UserSession>();

  public readonly tokens =
    new Map<string, StoredRefreshToken>();

  public async createSession(
    session: UserSession,
    refreshToken: StoredRefreshToken,
  ): Promise<void> {
    this.sessions.set(
      session.id,
      structuredClone(session),
    );

    this.tokens.set(
      refreshToken.id,
      structuredClone(refreshToken),
    );
  }

  public async findSessionById(
    sessionId: string,
  ): Promise<UserSession | null> {
    return this.sessions.get(sessionId) ?? null;
  }

  public async findRefreshTokenById(
    tokenId: string,
  ): Promise<StoredRefreshToken | null> {
    return this.tokens.get(tokenId) ?? null;
  }

  public async rotateRefreshTokenAtomically(
    input: AtomicRotationInput,
  ): Promise<RotationResult> {
    const session =
      this.sessions.get(input.sessionId);

    if (!session) {
      return "session_missing";
    }

    if (session.status !== "active") {
      return "session_revoked";
    }

    if (
      session.expiresAt.getTime() <=
      input.activityAt.getTime()
    ) {
      session.status = "expired";
      return "session_expired";
    }

    const currentToken =
      this.tokens.get(input.currentTokenId);

    if (!currentToken) {
      return "token_missing";
    }

    if (
      currentToken.sessionId !== input.sessionId ||
      currentToken.userId !== input.userId
    ) {
      return "token_mismatch";
    }

    if (currentToken.status !== "active") {
      session.status = "compromised";
      session.revokedAt = input.activityAt;
      session.revocationReason =
        "refresh_token_reuse_detected";

      return "token_reused";
    }

    if (
      currentToken.tokenHash !==
      input.currentTokenHash
    ) {
      session.status = "compromised";
      session.revokedAt = input.activityAt;
      session.revocationReason =
        "refresh_token_hash_mismatch";

      return "token_mismatch";
    }

    currentToken.status = "rotated";
    currentToken.rotatedAt = input.activityAt;
    currentToken.replacedByTokenId =
      input.replacementToken.id;

    this.tokens.set(
      input.replacementToken.id,
      structuredClone(
        input.replacementToken,
      ),
    );

    session.lastActivityAt = input.activityAt;

    return "rotated";
  }

  public async revokeSession(
    sessionId: string,
    reason: string,
    revokedAt: Date,
  ): Promise<boolean> {
    const session = this.sessions.get(sessionId);

    if (!session) {
      return false;
    }

    session.status =
      reason.includes("reuse")
        ? "compromised"
        : "revoked";

    session.revokedAt = revokedAt;
    session.revocationReason = reason;

    for (const token of this.tokens.values()) {
      if (
        token.sessionId === sessionId &&
        token.status === "active"
      ) {
        token.status = "revoked";
        token.revokedAt = revokedAt;
      }
    }

    return true;
  }

  public async revokeAllUserSessions(
    userId: string,
    reason: string,
    revokedAt: Date,
  ): Promise<number> {
    let revokedCount = 0;

    for (const session of this.sessions.values()) {
      if (
        session.userId === userId &&
        session.status === "active"
      ) {
        await this.revokeSession(
          session.id,
          reason,
          revokedAt,
        );

        revokedCount += 1;
      }
    }

    return revokedCount;
  }

  public async listActiveSessions(
    userId: string,
    now: Date,
  ): Promise<UserSession[]> {
    return [...this.sessions.values()]
      .filter(
        (session) =>
          session.userId === userId &&
          session.status === "active" &&
          session.expiresAt.getTime() >
            now.getTime(),
      )
      .map((session) =>
        structuredClone(session),
      );
  }
}

function createJwtService(): JwtService {
  return new JwtService({
    accessSecret: ACCESS_SECRET,
    refreshSecret: REFRESH_SECRET,
    issuer: "compound-trader-test",
    audience: "compound-trader-test-services",
    accessExpiresIn: "15m",
    refreshExpiresIn: "7d",
  });
}

function createService(
  repository: InMemorySessionRepository,
): SessionService {
  let id = 0;

  return new SessionService(
    repository,
    createJwtService(),
    new RefreshTokenHasher(),
    {
      sessionTtlDays: 7,
      now: () =>
        new Date("2026-07-28T09:00:00.000Z"),
      idGenerator: () => {
        id += 1;
        return `generated-id-${id}`;
      },
    },
  );
}

describe("SessionService", () => {
  it("creates a database-backed session", async () => {
    const repository =
      new InMemorySessionRepository();

    const service = createService(repository);

    const result = await service.createSession({
      userId: "user-001",
      roles: ["trader"],
      metadata: {
        deviceName: "Trading laptop",
        ipAddress: "127.0.0.1",
      },
    });

    expect(result.session.id).toBe(
      "generated-id-1",
    );

    expect(result.session.status).toBe("active");
    expect(result.accessToken).toBeTruthy();
    expect(result.refreshToken).toBeTruthy();
    expect(repository.sessions.size).toBe(1);
    expect(repository.tokens.size).toBe(1);

    const storedToken =
      [...repository.tokens.values()][0];

    expect(storedToken?.tokenHash).not.toBe(
      result.refreshToken,
    );
  });

  it("rotates a refresh token once", async () => {
    const repository =
      new InMemorySessionRepository();

    const service = createService(repository);

    const created = await service.createSession({
      userId: "user-002",
      roles: ["trader"],
    });

    const rotated =
      await service.rotateRefreshToken(
        created.refreshToken,
        ["trader"],
      );

    expect(rotated.refreshToken).not.toBe(
      created.refreshToken,
    );

    expect(repository.tokens.size).toBe(2);

    const oldToken =
      [...repository.tokens.values()]
        .find(
          (token) =>
            token.id === "generated-id-2",
        );

    expect(oldToken?.status).toBe("rotated");
    expect(oldToken?.replacedByTokenId).toBe(
      "generated-id-3",
    );
  });

  it("detects refresh-token replay", async () => {
    const repository =
      new InMemorySessionRepository();

    const service = createService(repository);

    const created = await service.createSession({
      userId: "user-003",
    });

    await service.rotateRefreshToken(
      created.refreshToken,
    );

    await expect(
      service.rotateRefreshToken(
        created.refreshToken,
      ),
    ).rejects.toMatchObject({
      code: "REFRESH_TOKEN_REUSED",
    } satisfies Partial<SessionSecurityError>);

    const storedSession =
      repository.sessions.get(
        created.session.id,
      );

    expect(storedSession?.status).toBe(
      "compromised",
    );
  });

  it("logs out the current session", async () => {
    const repository =
      new InMemorySessionRepository();

    const service = createService(repository);

    const created = await service.createSession({
      userId: "user-004",
    });

    const revoked = await service.logout(
      created.session.id,
    );

    expect(revoked).toBe(true);

    expect(
      repository.sessions.get(
        created.session.id,
      )?.status,
    ).toBe("revoked");
  });

  it("logs out all user devices", async () => {
    const repository =
      new InMemorySessionRepository();

    const service = createService(repository);

    await service.createSession({
      userId: "user-005",
    });

    await service.createSession({
      userId: "user-005",
    });

    await service.createSession({
      userId: "another-user",
    });

    const revokedCount =
      await service.logoutAllDevices("user-005");

    expect(revokedCount).toBe(2);

    const active =
      await service.listActiveSessions(
        "user-005",
      );

    expect(active).toHaveLength(0);
  });

  it("lists active sessions only", async () => {
    const repository =
      new InMemorySessionRepository();

    const service = createService(repository);

    const first = await service.createSession({
      userId: "user-006",
    });

    await service.createSession({
      userId: "user-006",
    });

    await service.logout(first.session.id);

    const active =
      await service.listActiveSessions(
        "user-006",
      );

    expect(active).toHaveLength(1);
    expect(active[0]?.status).toBe("active");
  });
});
