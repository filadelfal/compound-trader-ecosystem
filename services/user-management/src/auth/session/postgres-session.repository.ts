import { randomUUID } from "node:crypto";

import type {
  Pool,
  PoolClient,
} from "pg";

import {
  pool,
} from "../../db";

import type {
  AtomicRotationInput,
  RotationResult,
  SessionRepository,
  StoredRefreshToken,
  UserSession,
} from "./session.types";

interface SessionRow {
  id: string;
  user_id: string;
  status: UserSession["status"];
  device_id: string | null;
  device_name: string | null;
  browser: string | null;
  operating_system: string | null;
  ip_address: string | null;
  user_agent: string | null;
  country_code: string | null;
  created_at: Date;
  last_activity_at: Date;
  expires_at: Date;
  revoked_at: Date | null;
  revocation_reason: string | null;
}

interface RefreshTokenRow {
  id: string;
  session_id: string;
  user_id: string;
  token_hash: string;
  status: StoredRefreshToken["status"];
  created_at: Date;
  expires_at: Date;
  rotated_at: Date | null;
  replaced_by_token_id: string | null;
  revoked_at: Date | null;
}

interface SecurityEventInput {
  userId?: string;
  sessionId?: string;
  eventType: string;
  severity: "info" | "warning" | "critical";
  ipAddress?: string;
  userAgent?: string;
  metadata?: Record<string, unknown>;
}

function mapSession(
  row: SessionRow,
): UserSession {
  return {
    id: row.id,
    userId: row.user_id,
    status: row.status,
    metadata: {
      deviceId: row.device_id ?? undefined,
      deviceName: row.device_name ?? undefined,
      browser: row.browser ?? undefined,
      operatingSystem:
        row.operating_system ?? undefined,
      ipAddress: row.ip_address ?? undefined,
      userAgent: row.user_agent ?? undefined,
      countryCode: row.country_code ?? undefined,
    },
    createdAt: row.created_at,
    lastActivityAt: row.last_activity_at,
    expiresAt: row.expires_at,
    revokedAt: row.revoked_at ?? undefined,
    revocationReason:
      row.revocation_reason ?? undefined,
  };
}

function mapRefreshToken(
  row: RefreshTokenRow,
): StoredRefreshToken {
  return {
    id: row.id,
    sessionId: row.session_id,
    userId: row.user_id,
    tokenHash: row.token_hash,
    status: row.status,
    createdAt: row.created_at,
    expiresAt: row.expires_at,
    rotatedAt: row.rotated_at ?? undefined,
    replacedByTokenId:
      row.replaced_by_token_id ?? undefined,
    revokedAt: row.revoked_at ?? undefined,
  };
}

export class PostgresSessionRepository
  implements SessionRepository {
  public constructor(
    private readonly database: Pool = pool,
  ) {}

  public async createSession(
    session: UserSession,
    refreshToken: StoredRefreshToken,
  ): Promise<void> {
    const client = await this.database.connect();

    try {
      await client.query("BEGIN");

      await client.query(
        `
          INSERT INTO user_sessions (
            id,
            user_id,
            status,
            device_id,
            device_name,
            browser,
            operating_system,
            ip_address,
            user_agent,
            country_code,
            created_at,
            last_activity_at,
            expires_at,
            revoked_at,
            revocation_reason
          )
          VALUES (
            $1, $2, $3, $4, $5,
            $6, $7, $8, $9, $10,
            $11, $12, $13, $14, $15
          )
        `,
        [
          session.id,
          session.userId,
          session.status,
          session.metadata.deviceId ?? null,
          session.metadata.deviceName ?? null,
          session.metadata.browser ?? null,
          session.metadata.operatingSystem ?? null,
          session.metadata.ipAddress ?? null,
          session.metadata.userAgent ?? null,
          session.metadata.countryCode ?? null,
          session.createdAt,
          session.lastActivityAt,
          session.expiresAt,
          session.revokedAt ?? null,
          session.revocationReason ?? null,
        ],
      );

      await this.insertRefreshToken(
        client,
        refreshToken,
      );

      await this.insertSecurityEvent(
        client,
        {
          userId: session.userId,
          sessionId: session.id,
          eventType: "SESSION_CREATED",
          severity: "info",
          ipAddress:
            session.metadata.ipAddress,
          userAgent:
            session.metadata.userAgent,
          metadata: {
            deviceId:
              session.metadata.deviceId,
            deviceName:
              session.metadata.deviceName,
          },
        },
      );

      await client.query("COMMIT");
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }
  }

  public async findSessionById(
    sessionId: string,
  ): Promise<UserSession | null> {
    const result =
      await this.database.query<SessionRow>(
        `
          SELECT *
          FROM user_sessions
          WHERE id = $1
        `,
        [sessionId],
      );

    const row = result.rows[0];

    return row ? mapSession(row) : null;
  }

  public async findRefreshTokenById(
    tokenId: string,
  ): Promise<StoredRefreshToken | null> {
    const result =
      await this.database.query<RefreshTokenRow>(
        `
          SELECT *
          FROM refresh_tokens
          WHERE id = $1
        `,
        [tokenId],
      );

    const row = result.rows[0];

    return row ? mapRefreshToken(row) : null;
  }

  public async rotateRefreshTokenAtomically(
    input: AtomicRotationInput,
  ): Promise<RotationResult> {
    const client = await this.database.connect();

    try {
      await client.query("BEGIN");

      const sessionResult =
        await client.query<SessionRow>(
          `
            SELECT *
            FROM user_sessions
            WHERE id = $1
            FOR UPDATE
          `,
          [input.sessionId],
        );

      const session = sessionResult.rows[0];

      if (!session) {
        await client.query("ROLLBACK");
        return "session_missing";
      }

      if (session.user_id !== input.userId) {
        await this.compromiseSession(
          client,
          input.sessionId,
          input.userId,
          input.activityAt,
          "session_user_mismatch",
        );

        await client.query("COMMIT");

        return "token_mismatch";
      }

      if (session.status !== "active") {
        await client.query("ROLLBACK");
        return "session_revoked";
      }

      if (
        session.expires_at.getTime() <=
        input.activityAt.getTime()
      ) {
        await client.query(
          `
            UPDATE user_sessions
            SET
              status = 'expired',
              revoked_at = $2,
              revocation_reason =
                'session_expired'
            WHERE id = $1
          `,
          [
            input.sessionId,
            input.activityAt,
          ],
        );

        await client.query(
          `
            UPDATE refresh_tokens
            SET
              status = 'expired',
              revoked_at = $2
            WHERE
              session_id = $1
              AND status = 'active'
          `,
          [
            input.sessionId,
            input.activityAt,
          ],
        );

        await this.insertSecurityEvent(
          client,
          {
            userId: input.userId,
            sessionId: input.sessionId,
            eventType: "SESSION_EXPIRED",
            severity: "info",
          },
        );

        await client.query("COMMIT");

        return "session_expired";
      }

      const tokenResult =
        await client.query<RefreshTokenRow>(
          `
            SELECT *
            FROM refresh_tokens
            WHERE id = $1
            FOR UPDATE
          `,
          [input.currentTokenId],
        );

      const currentToken = tokenResult.rows[0];

      if (!currentToken) {
        await client.query("ROLLBACK");
        return "token_missing";
      }

      if (
        currentToken.session_id !==
          input.sessionId ||
        currentToken.user_id !== input.userId
      ) {
        await this.compromiseSession(
          client,
          input.sessionId,
          input.userId,
          input.activityAt,
          "refresh_token_identity_mismatch",
        );

        await client.query("COMMIT");

        return "token_mismatch";
      }

      if (currentToken.status !== "active") {
        await this.compromiseSession(
          client,
          input.sessionId,
          input.userId,
          input.activityAt,
          "refresh_token_reuse_detected",
        );

        await client.query("COMMIT");

        return "token_reused";
      }

      if (
        currentToken.token_hash !==
        input.currentTokenHash
      ) {
        await this.compromiseSession(
          client,
          input.sessionId,
          input.userId,
          input.activityAt,
          "refresh_token_hash_mismatch",
        );

        await client.query("COMMIT");

        return "token_mismatch";
      }

      const updateResult = await client.query(
        `
          UPDATE refresh_tokens
          SET
            status = 'rotated',
            rotated_at = $2,
            replaced_by_token_id = $3
          WHERE
            id = $1
            AND status = 'active'
        `,
        [
          input.currentTokenId,
          input.activityAt,
          input.replacementToken.id,
        ],
      );

      if (updateResult.rowCount !== 1) {
        await this.compromiseSession(
          client,
          input.sessionId,
          input.userId,
          input.activityAt,
          "concurrent_refresh_detected",
        );

        await client.query("COMMIT");

        return "token_reused";
      }

      await this.insertRefreshToken(
        client,
        input.replacementToken,
      );

      await client.query(
        `
          UPDATE user_sessions
          SET last_activity_at = $2
          WHERE id = $1
        `,
        [
          input.sessionId,
          input.activityAt,
        ],
      );

      await this.insertSecurityEvent(
        client,
        {
          userId: input.userId,
          sessionId: input.sessionId,
          eventType: "REFRESH_TOKEN_ROTATED",
          severity: "info",
          metadata: {
            previousTokenId:
              input.currentTokenId,
            replacementTokenId:
              input.replacementToken.id,
          },
        },
      );

      await client.query("COMMIT");

      return "rotated";
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }
  }

  public async revokeSession(
    sessionId: string,
    reason: string,
    revokedAt: Date,
  ): Promise<boolean> {
    const client = await this.database.connect();

    try {
      await client.query("BEGIN");

      const result = await client.query<{
        user_id: string;
      }>(
        `
          UPDATE user_sessions
          SET
            status = CASE
              WHEN $2 LIKE '%reuse%'
                OR $2 LIKE '%mismatch%'
                OR $2 LIKE '%compromised%'
              THEN 'compromised'
              ELSE 'revoked'
            END,
            revoked_at = $3,
            revocation_reason = $2
          WHERE
            id = $1
            AND status = 'active'
          RETURNING user_id
        `,
        [
          sessionId,
          reason,
          revokedAt,
        ],
      );

      const updated = result.rows[0];

      if (!updated) {
        await client.query("ROLLBACK");
        return false;
      }

      await client.query(
        `
          UPDATE refresh_tokens
          SET
            status = 'revoked',
            revoked_at = $2
          WHERE
            session_id = $1
            AND status = 'active'
        `,
        [
          sessionId,
          revokedAt,
        ],
      );

      await this.insertSecurityEvent(
        client,
        {
          userId: updated.user_id,
          sessionId,
          eventType:
            reason === "user_logout"
              ? "LOGOUT"
              : "SESSION_REVOKED",
          severity:
            reason.includes("reuse") ||
            reason.includes("mismatch")
              ? "critical"
              : "info",
          metadata: {
            reason,
          },
        },
      );

      await client.query("COMMIT");

      return true;
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }
  }

  public async revokeAllUserSessions(
    userId: string,
    reason: string,
    revokedAt: Date,
  ): Promise<number> {
    const client = await this.database.connect();

    try {
      await client.query("BEGIN");

      const result = await client.query<{
        id: string;
      }>(
        `
          UPDATE user_sessions
          SET
            status = 'revoked',
            revoked_at = $2,
            revocation_reason = $3
          WHERE
            user_id = $1
            AND status = 'active'
          RETURNING id
        `,
        [
          userId,
          revokedAt,
          reason,
        ],
      );

      const sessionIds =
        result.rows.map((row) => row.id);

      if (sessionIds.length > 0) {
        await client.query(
          `
            UPDATE refresh_tokens
            SET
              status = 'revoked',
              revoked_at = $2
            WHERE
              user_id = $1
              AND status = 'active'
          `,
          [
            userId,
            revokedAt,
          ],
        );

        await this.insertSecurityEvent(
          client,
          {
            userId,
            eventType: "LOGOUT_ALL_DEVICES",
            severity: "info",
            metadata: {
              reason,
              revokedSessionCount:
                sessionIds.length,
              sessionIds,
            },
          },
        );
      }

      await client.query("COMMIT");

      return sessionIds.length;
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }
  }

  public async listActiveSessions(
    userId: string,
    now: Date,
  ): Promise<UserSession[]> {
    const result =
      await this.database.query<SessionRow>(
        `
          SELECT *
          FROM user_sessions
          WHERE
            user_id = $1
            AND status = 'active'
            AND expires_at > $2
          ORDER BY last_activity_at DESC
        `,
        [
          userId,
          now,
        ],
      );

    return result.rows.map(mapSession);
  }

  private async insertRefreshToken(
    client: PoolClient,
    token: StoredRefreshToken,
  ): Promise<void> {
    await client.query(
      `
        INSERT INTO refresh_tokens (
          id,
          session_id,
          user_id,
          token_hash,
          status,
          created_at,
          expires_at,
          rotated_at,
          replaced_by_token_id,
          revoked_at
        )
        VALUES (
          $1, $2, $3, $4, $5,
          $6, $7, $8, $9, $10
        )
      `,
      [
        token.id,
        token.sessionId,
        token.userId,
        token.tokenHash,
        token.status,
        token.createdAt,
        token.expiresAt,
        token.rotatedAt ?? null,
        token.replacedByTokenId ?? null,
        token.revokedAt ?? null,
      ],
    );
  }

  private async compromiseSession(
    client: PoolClient,
    sessionId: string,
    userId: string,
    compromisedAt: Date,
    reason: string,
  ): Promise<void> {
    await client.query(
      `
        UPDATE user_sessions
        SET
          status = 'compromised',
          revoked_at = $2,
          revocation_reason = $3
        WHERE id = $1
      `,
      [
        sessionId,
        compromisedAt,
        reason,
      ],
    );

    await client.query(
      `
        UPDATE refresh_tokens
        SET
          status = 'revoked',
          revoked_at = $2
        WHERE
          session_id = $1
          AND status = 'active'
      `,
      [
        sessionId,
        compromisedAt,
      ],
    );

    await this.insertSecurityEvent(
      client,
      {
        userId,
        sessionId,
        eventType: "REFRESH_TOKEN_REPLAY",
        severity: "critical",
        metadata: {
          reason,
        },
      },
    );
  }

  private async insertSecurityEvent(
    client: PoolClient,
    input: SecurityEventInput,
  ): Promise<void> {
    await client.query(
      `
        INSERT INTO authentication_security_events (
          id,
          user_id,
          session_id,
          event_type,
          severity,
          ip_address,
          user_agent,
          metadata,
          created_at
        )
        VALUES (
          $1, $2, $3, $4, $5,
          $6, $7, $8::jsonb, NOW()
        )
      `,
      [
        randomUUID(),
        input.userId ?? null,
        input.sessionId ?? null,
        input.eventType,
        input.severity,
        input.ipAddress ?? null,
        input.userAgent ?? null,
        JSON.stringify(input.metadata ?? {}),
      ],
    );
  }
}

export const postgresSessionRepository =
  new PostgresSessionRepository(pool);
