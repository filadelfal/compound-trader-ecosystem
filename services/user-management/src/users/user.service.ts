import { Pool } from "pg";

export interface UpdateCurrentUser {
  displayName?: string | null;
  firstName?: string | null;
  lastName?: string | null;
  phoneNumber?: string | null;
  countryCode?: string | null;
  timezone?: string;
  locale?: string;
  avatarUrl?: string | null;
  bio?: string | null;
}

export class UserService {
  public constructor(private readonly pool: Pool) {}

  public async getCurrentUser(userId: string) {
    const result = await this.pool.query(
      `SELECT users.id, users.email, users.display_name, users.status,
              users.email_verified, users.created_at,
              profiles.first_name, profiles.last_name, profiles.phone_number,
              profiles.country_code, profiles.timezone, profiles.locale,
              profiles.avatar_url, profiles.bio,
              COALESCE(array_agg(DISTINCT roles.name)
                FILTER (WHERE roles.name IS NOT NULL), '{}') AS roles
         FROM compound.users AS users
         LEFT JOIN compound.user_profiles AS profiles ON profiles.user_id = users.id
         LEFT JOIN compound.user_roles AS user_roles ON user_roles.user_id = users.id
         LEFT JOIN compound.roles AS roles ON roles.id = user_roles.role_id
        WHERE users.id = $1 AND users.deleted_at IS NULL
        GROUP BY users.id, profiles.user_id`,
      [userId],
    );
    return result.rows[0] ?? null;
  }

  public async updateCurrentUser(userId: string, input: UpdateCurrentUser) {
    const client = await this.pool.connect();
    try {
      await client.query("BEGIN");
      if (input.displayName !== undefined) {
        await client.query(
          "UPDATE compound.users SET display_name = $2 WHERE id = $1 AND deleted_at IS NULL",
          [userId, input.displayName],
        );
      }
      await client.query(
        `INSERT INTO compound.user_profiles (
           user_id, first_name, last_name, phone_number, country_code,
           timezone, locale, avatar_url, bio
         ) VALUES ($1,$2,$3,$4,$5,COALESCE($6,'UTC'),COALESCE($7,'en'),$8,$9)
         ON CONFLICT (user_id) DO UPDATE SET
           first_name = COALESCE($2, compound.user_profiles.first_name),
           last_name = COALESCE($3, compound.user_profiles.last_name),
           phone_number = COALESCE($4, compound.user_profiles.phone_number),
           country_code = COALESCE($5, compound.user_profiles.country_code),
           timezone = COALESCE($6, compound.user_profiles.timezone),
           locale = COALESCE($7, compound.user_profiles.locale),
           avatar_url = COALESCE($8, compound.user_profiles.avatar_url),
           bio = COALESCE($9, compound.user_profiles.bio)`,
        [
          userId, input.firstName, input.lastName, input.phoneNumber,
          input.countryCode, input.timezone, input.locale, input.avatarUrl,
          input.bio,
        ],
      );
      await client.query(
        `INSERT INTO compound.audit_logs
           (user_id, actor_user_id, event_type, entity_type, entity_id)
         VALUES ($1, $1, 'user.profile_updated', 'user', $1)`,
        [userId],
      );
      await client.query("COMMIT");
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }
    return this.getCurrentUser(userId);
  }
}
