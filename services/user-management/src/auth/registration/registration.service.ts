import { createHash, randomBytes } from "node:crypto";
import type { Pool, PoolClient } from "pg";

import { config } from "../../config";
import type { EmailSender } from "../email/email.service";
import { PasswordPolicyError, PasswordService } from "../password/password.service";

export class RegistrationConflictError extends Error {}
export class VerificationTokenError extends Error {}

export interface RegistrationInput {
  email: string;
  password: string;
  displayName?: string;
}

function hashToken(token: string): string {
  return createHash("sha256").update(token, "utf8").digest("hex");
}

export class RegistrationService {
  public constructor(
    private readonly database: Pool,
    private readonly passwords: PasswordService,
    private readonly emails: EmailSender,
  ) {}

  public async register(input: RegistrationInput): Promise<{ userId: string }> {
    const email = input.email.trim().toLowerCase();
    const passwordHash = await this.passwords.hash(input.password);
    const rawToken = randomBytes(32).toString("base64url");
    const client = await this.database.connect();

    try {
      await client.query("BEGIN");
      const user = await client.query<{ id: string }>(
        `INSERT INTO compound.users (email, password_hash, display_name)
         VALUES ($1, $2, $3)
         RETURNING id`,
        [email, passwordHash, input.displayName?.trim() || null],
      );
      const userId = user.rows[0].id;
      await this.createVerificationToken(client, userId, rawToken);
      await client.query(
        `INSERT INTO compound.user_roles (user_id, role_id)
         SELECT $1, id FROM compound.roles WHERE name = 'user'`,
        [userId],
      );
      await client.query(
        `INSERT INTO compound.audit_logs (user_id, event_type, entity_type, entity_id)
         VALUES ($1, 'auth.registration.created', 'user', $1)`,
        [userId],
      );
      await client.query("COMMIT");

      await this.emails.send({
        to: email,
        subject: "Verify your Compound Trader account",
        text: `Verify your account using this token: ${rawToken}`,
      });
      return { userId };
    } catch (error) {
      await client.query("ROLLBACK");
      if (
        typeof error === "object" &&
        error !== null &&
        "code" in error &&
        error.code === "23505"
      ) {
        throw new RegistrationConflictError("An account already exists for this email.");
      }
      throw error;
    } finally {
      client.release();
    }
  }

  public async verifyEmail(token: string): Promise<void> {
    const client = await this.database.connect();
    try {
      await client.query("BEGIN");
      const result = await client.query<{ user_id: string }>(
        `UPDATE compound.email_verification_tokens
         SET used_at = NOW()
         WHERE token_hash = $1 AND used_at IS NULL AND expires_at > NOW()
         RETURNING user_id`,
        [hashToken(token)],
      );
      if (result.rowCount !== 1) {
        throw new VerificationTokenError("Verification token is invalid or expired.");
      }
      const userId = result.rows[0].user_id;
      await client.query(
        `UPDATE compound.users
         SET email_verified = TRUE, email_verified_at = NOW(), status = 'active'
         WHERE id = $1 AND deleted_at IS NULL`,
        [userId],
      );
      await client.query(
        `INSERT INTO compound.audit_logs (user_id, event_type, entity_type, entity_id)
         VALUES ($1, 'auth.email.verified', 'user', $1)`,
        [userId],
      );
      await client.query("COMMIT");
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }
  }

  private async createVerificationToken(
    client: PoolClient,
    userId: string,
    rawToken: string,
  ): Promise<void> {
    await client.query(
      `INSERT INTO compound.email_verification_tokens (user_id, token_hash, expires_at)
       VALUES ($1, $2, NOW() + ($3 * INTERVAL '1 hour'))`,
      [userId, hashToken(rawToken), config.EMAIL_VERIFICATION_EXPIRES_HOURS],
    );
  }
}

export { PasswordPolicyError };
