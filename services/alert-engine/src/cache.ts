import { createClient } from "redis";
import { config } from "./config";

export const redis = createClient({ url: config.REDIS_URL });

export async function connectCache(): Promise<void> {
  if (!redis.isOpen) {
    await redis.connect();
  }
}

export async function checkCache(): Promise<void> {
  await connectCache();
  await redis.ping();
}

export async function closeCache(): Promise<void> {
  if (redis.isOpen) {
    await redis.quit();
  }
}
