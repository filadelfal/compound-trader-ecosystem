import { Pool } from "pg";
import { config } from "./config";

export const pool = new Pool({ connectionString: config.DATABASE_URL });

export async function checkDatabase(): Promise<void> {
  await pool.query("SELECT 1");
}

export async function closeDatabase(): Promise<void> {
  await pool.end();
}
