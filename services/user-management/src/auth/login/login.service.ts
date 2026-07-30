import type { Pool, PoolClient } from "pg";

import { config } from "../../config";
import { PasswordService } from "../password/password.service";
import { SessionService } from "../session/session.service";
import type { CreatedSession, SessionDeviceMetadata } from "../session/session.types";

export class InvalidCredentialsError extends Error {}
export class AccountLockedError extends Error {}
export class AccountUnavailableError extends Error {}

interface UserRow {
  id: string;
  password_hash: string;
  status: string;
  email_verified: boolean;
  failed_login_attempts: number;
  locked_until: Date | null;
}

export interface LoginInput {
  email: string;
  password: string;
  metadata?: SessionDeviceMetadata;
}

export class LoginService {
  public constructor(
    private readonly database: Pool,
    private readonly passwords: PasswordService,
    private readonly sessions: SessionService,
  ) {}

  public async login(input: LoginInput): Promise<CreatedSession> {
    const email = input.email.trim().toLowerCase();
    const client = await this.database.connect();
    let user: UserRow | undefined;
    let roles: string[] = [];
    let committed = false;

    try {
      await client.query("BEGIN");
      const result = await client.query<UserRow>(
        `SELECT id, password_hash, status, email_verified,
                failed_login_attempts, locked_until
         FROM compound.users
         WHERE LOWER(email) = $1 AND deleted_at IS NULL
         FOR UPDATE`,
        [email],
      );
      user = result.rows[0];

      if (!user) {
        await this.recordAttempt(client, null, email, false, "invalid_credentials", input.metadata);
        await client.query("COMMIT");
        committed = true;
        throw new InvalidCredentialsError("Invalid email or password.");
      }

      if (user.locked_until && user.locked_until.getTime() > Date.now()) {
        await this.recordAttempt(client, user.id, email, false, "account_locked", input.metadata);
        await client.query("COMMIT");
        committed = true;
        throw new AccountLockedError("Account is temporarily locked.");
      }

      const validPassword = await this.passwords.verify(user.password_hash, input.password);
      if (!validPassword) {
        const attempts = user.failed_login_attempts + 1;
        const shouldLock = attempts >= config.ACCOUNT_LOCK_THRESHOLD;
        await client.query(
          `UPDATE compound.users
           SET failed_login_attempts = $2,
               locked_until = CASE WHEN $3 THEN NOW() + ($4 * INTERVAL '1 minute') ELSE NULL END,
               status = CASE WHEN $3 THEN 'locked' ELSE status END
           WHERE id = $1`,
          [user.id, attempts, shouldLock, config.ACCOUNT_LOCK_MINUTES],
        );
        await this.recordAttempt(
          client,
          user.id,
          email,
          false,
          shouldLock ? "account_locked" : "invalid_credentials",
          input.metadata,
        );
        await this.audit(
          client,
          user.id,
          shouldLock ? "auth.account.locked" : "auth.login.failed",
          input.metadata,
        );
        await client.query("COMMIT");
        committed = true;
        if (shouldLock) {
          throw new AccountLockedError("Account is temporarily locked.");
        }
        throw new InvalidCredentialsError("Invalid email or password.");
      }

      if (!user.email_verified || !["active", "locked"].includes(user.status)) {
        await this.recordAttempt(client, user.id, email, false, "account_unavailable", input.metadata);
        await client.query("COMMIT");
        committed = true;
        throw new AccountUnavailableError("Account is not available for login.");
      }

      const roleResult = await client.query<{ name: string }>(
        `SELECT roles.name FROM compound.user_roles
         JOIN compound.roles ON roles.id = user_roles.role_id
         WHERE user_roles.user_id = $1 ORDER BY roles.name`,
        [user.id],
      );
      roles = roleResult.rows.map(({ name }) => name);
      await client.query(
        `UPDATE compound.users SET failed_login_attempts = 0, locked_until = NULL,
         status = 'active', last_login_at = NOW() WHERE id = $1`,
        [user.id],
      );
      await this.recordAttempt(client, user.id, email, true, null, input.metadata);
      await this.audit(client, user.id, "auth.login.succeeded", input.metadata);
      await client.query("COMMIT");
      committed = true;
    } catch (error) {
      if (!committed) {
        await client.query("ROLLBACK");
      }
      throw error;
    } finally {
      client.release();
    }

    return this.sessions.createSession({
      userId: user.id,
      roles,
      metadata: input.metadata,
    });
  }

  private async recordAttempt(
    client: PoolClient,
    userId: string | null,
    email: string,
    successful: boolean,
    failureReason: string | null,
    metadata?: SessionDeviceMetadata,
  ): Promise<void> {
    await client.query(
      `INSERT INTO compound.login_attempts
       (user_id, attempted_email, successful, failure_reason, ip_address, user_agent)
       VALUES ($1, $2, $3, $4, $5, $6)`,
      [userId, email, successful, failureReason, metadata?.ipAddress ?? null, metadata?.userAgent ?? null],
    );
  }

  private async audit(
    client: PoolClient,
    userId: string,
    eventType: string,
    metadata?: SessionDeviceMetadata,
  ): Promise<void> {
    await client.query(
      `INSERT INTO compound.audit_logs
       (user_id, actor_user_id, event_type, entity_type, entity_id, ip_address, user_agent)
       VALUES ($1, $1, $2, 'user', $1, $3, $4)`,
      [userId, eventType, metadata?.ipAddress ?? null, metadata?.userAgent ?? null],
    );
  }
}
