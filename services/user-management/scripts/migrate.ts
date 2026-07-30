import {
  createHash,
} from "node:crypto";

import {
  readFile,
  readdir,
} from "node:fs/promises";

import {
  resolve,
} from "node:path";

import {
  pool,
} from "../src/db";

interface MigrationRecord {
  filename: string;
  checksum: string;
}

function checksum(
  content: string,
): string {
  return createHash("sha256")
    .update(content, "utf8")
    .digest("hex");
}

async function migrate(): Promise<void> {
  const migrationDirectory =
    resolve(process.cwd(), "migrations");

  await pool.query(`
    CREATE TABLE IF NOT EXISTS schema_migrations (
      filename TEXT PRIMARY KEY,
      checksum CHAR(64) NOT NULL,
      applied_at TIMESTAMPTZ NOT NULL
        DEFAULT NOW()
    )
  `);

  const filenames = (
    await readdir(migrationDirectory)
  )
    .filter((name) => name.endsWith(".sql"))
    .sort((a, b) => a.localeCompare(b));

  for (const filename of filenames) {
    const fullPath =
      resolve(migrationDirectory, filename);

    const sql = await readFile(
      fullPath,
      "utf8",
    );

    const migrationChecksum =
      checksum(sql);

    const existing =
      await pool.query<MigrationRecord>(
        `
          SELECT filename, checksum
          FROM schema_migrations
          WHERE filename = $1
        `,
        [filename],
      );

    const record = existing.rows[0];

    if (record) {
      if (
        record.checksum !== migrationChecksum
      ) {
        throw new Error(
          `Migration checksum mismatch: ${filename}`,
        );
      }

      console.log(
        `Already applied: ${filename}`,
      );

      continue;
    }

    console.log(`Applying: ${filename}`);

    /*
     * Existing SQL migration files may contain
     * their own BEGIN and COMMIT statements.
     */
    await pool.query(sql);

    await pool.query(
      `
        INSERT INTO schema_migrations (
          filename,
          checksum
        )
        VALUES ($1, $2)
      `,
      [
        filename,
        migrationChecksum,
      ],
    );

    console.log(`Applied: ${filename}`);
  }
}

migrate()
  .then(async () => {
    await pool.end();
    console.log("Migrations completed.");
  })
  .catch(async (error: unknown) => {
    console.error(
      "Migration failed:",
      error instanceof Error
        ? error.message
        : String(error),
    );

    await pool.end();
    process.exitCode = 1;
  });
