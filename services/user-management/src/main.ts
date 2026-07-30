import { app, emailOutboxWorker } from "./app";
import { config } from "./config";
import { logger } from "./logger";
import { connectCache, closeCache } from "./cache";
import { closeDatabase } from "./db";

async function start(): Promise<void> {
  await connectCache();
  emailOutboxWorker.start();

  const server = app.listen(config.PORT, "0.0.0.0", () => {
    logger.info({ event: "service_started", port: config.PORT });
  });

  async function shutdown(signal: string): Promise<void> {
    logger.info({ event: "shutdown_started", signal });
    emailOutboxWorker.stop();
    server.close(async () => {
      await Promise.allSettled([closeCache(), closeDatabase()]);
      logger.info({ event: "shutdown_complete" });
      process.exit(0);
    });
  }

  process.on("SIGTERM", () => void shutdown("SIGTERM"));
  process.on("SIGINT", () => void shutdown("SIGINT"));
}

start().catch((error) => {
  logger.error({ event: "startup_failed", error: error instanceof Error ? error.message : String(error) });
  process.exit(1);
});
