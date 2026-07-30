import { createHash, randomBytes } from "node:crypto";
import type { Pool } from "pg";

import { config } from "../../config";
import type { EmailSender } from "../email/email.service";
import { passwordResetEmail } from "../email/email.templates";
import { PasswordPolicyError, PasswordService } from "../password/password.service";

export class PasswordResetTokenError extends Error {}

function hashToken(token: string): string {
  return createHash("sha256").update(token, "utf8").digest("hex");
}

export class PasswordResetService {
  public constructor(
    private readonly database: Pool,
    private readonly passwords: PasswordService,
    private readonly emails: EmailSender,
  ) {}

  public async requestReset(emailInput: string, requestedIp?: string): Promise<void> {
    const email = emailInput.trim().toLowerCase();
    const rawToken = randomBytes(32).toString("base64url");
    const client = await this.database.connect();
    let recipient: string | null = null;

    try {
      await client.query("BEGIN");
      const result = await client.query<{ id: string; email: string }>(
        `SELECT id, email
           FROM compound.users
          WHERE LOWER(email) = $1 AND deleted_at IS NULL
            AND status NOT IN ('disabled', 'suspended')
          FOR UPDATE`,
        [email],
      );
      const user = result.rows[0];
      if (user) {
        await client.query(
          `UPDATE compound.password_reset_tokens
              SET used_at = NOW()
            WHERE user_id = $1 AND used_at IS NULL`,
          [user.id],
        );
        await client.query(
          `INSERT INTO compound.password_reset_tokens
             (user_id, token_hash, expires_at, requested_ip)
           VALUES ($1, $2, NOW() + ($3 * INTERVAL '1 minute'), $4)`,
          [
            user.id,
            hashToken(rawToken),
            config.PASSWORD_RESET_EXPIRES_MINUTES,
            requestedIp || null,
          ],
        );
        await client.query(
          `INSERT INTO compound.audit_logs
             (user_id, event_type, entity_type, entity_id, ip_address)
           VALUES ($1, 'auth.password_reset.requested', 'user', $1, $2)`,
          [user.id, requestedIp || null],
        );
        recipient = user.email;
      }
      await client.query("COMMIT");
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }

    if (recipient) {
      await this.emails.send({
        to: recipient,
        ...passwordResetEmail(rawToken),
      });
    }
  }

  public async resetPassword(
    token: string,
    newPassword: string,
    ipAddress?: string,
  ): Promise<void> {
    const passwordHash = await this.passwords.hash(newPassword);
    const client = await this.database.connect();

    try {
      await client.query("BEGIN");
      const result = await client.query<{ user_id: string }>(
        `UPDATE compound.password_reset_tokens
            SET used_at = NOW()
          WHERE token_hash = $1 AND used_at IS NULL AND expires_at > NOW()
          RETURNING user_id`,
        [hashToken(token)],
      );
      if (result.rowCount !== 1) {
        throw new PasswordResetTokenError("Password reset token is invalid or expired.");
      }

      const userId = result.rows[0].user_id;
      const updated = await client.query(
        `UPDATE compound.users
            SET password_hash = $1,
                password_changed_at = NOW(),
                failed_login_attempts = 0,
                locked_until = NULL,
                status = CASE WHEN status = 'locked' THEN 'active' ELSE status END
          WHERE id = $2 AND deleted_at IS NULL`,
        [passwordHash, userId],
      );
      if (updated.rowCount !== 1) {
        throw new PasswordResetTokenError("Password reset token is invalid or expired.");
      }

      await client.query(
        `UPDATE compound.refresh_tokens
            SET revoked_at = COALESCE(revoked_at, NOW())
          WHERE user_id = $1 AND revoked_at IS NULL`,
        [userId],
      );
      await client.query(
        `UPDATE compound.password_reset_tokens
            SET used_at = COALESCE(used_at, NOW())
          WHERE user_id = $1`,
        [userId],
      );
      await client.query(
        `INSERT INTO compound.audit_logs
           (user_id, event_type, entity_type, entity_id, ip_address)
         VALUES ($1, 'auth.password_reset.completed', 'user', $1, $2)`,
        [userId, ipAddress || null],
      );
      await client.query("COMMIT");
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }
  }
}

export { PasswordPolicyError };

