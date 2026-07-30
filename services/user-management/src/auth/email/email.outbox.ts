import type { Pool, PoolClient } from "pg";

import { logger } from "../../logger";
import type { EmailMessage, EmailSender } from "./email.service";
import { emailOutboxMetrics } from "./email.metrics";

export interface EmailQueue {
  enqueue(client: PoolClient, message: EmailMessage): Promise<void>;
}

export class PostgresEmailQueue implements EmailQueue {
  public async enqueue(client: PoolClient, message: EmailMessage): Promise<void> {
    await client.query(
      `INSERT INTO compound.email_outbox
         (recipient, subject, text_body, html_body)
       VALUES ($1, $2, $3, $4)`,
      [message.to, message.subject, message.text, message.html ?? null],
    );
  }
}

interface OutboxRecord {
  id: string;
  recipient: string;
  subject: string;
  text_body: string;
  html_body: string | null;
  attempt_count: number;
}

export interface EmailOutboxWorkerOptions {
  pollIntervalMs: number;
  batchSize: number;
  maxAttempts: number;
  retryBaseSeconds: number;
}

export class EmailOutboxWorker {
  private timer: NodeJS.Timeout | undefined;
  private running = false;

  public constructor(
    private readonly database: Pool,
    private readonly sender: EmailSender,
    private readonly options: EmailOutboxWorkerOptions,
  ) {}

  public start(): void {
    if (this.timer) return;
    this.timer = setInterval(() => void this.runSafely(), this.options.pollIntervalMs);
    this.timer.unref();
    void this.runSafely();
  }

  public stop(): void {
    if (this.timer) clearInterval(this.timer);
    this.timer = undefined;
  }

  public async processBatch(): Promise<number> {
    if (this.running) return 0;
    this.running = true;
    const stopTimer = emailOutboxMetrics.batchDuration.startTimer();
    try {
      const records = await this.claimBatch();
      emailOutboxMetrics.claimed.inc(records.length);
      for (const record of records) {
        await this.deliver(record);
      }
      return records.length;
    } finally {
      stopTimer();
      this.running = false;
    }
  }

  private async runSafely(): Promise<void> {
    try {
      await this.processBatch();
    } catch (error) {
      logger.error({
        event: "email_outbox_worker_failed",
        error: error instanceof Error ? error.name : "UnknownError",
      });
    }
  }

  private async claimBatch(): Promise<OutboxRecord[]> {
    const client = await this.database.connect();
    try {
      await client.query("BEGIN");
      const result = await client.query<OutboxRecord>(
        `WITH candidates AS (
           SELECT id
             FROM compound.email_outbox
            WHERE (
                    status IN ('pending', 'failed')
                    OR (status = 'processing' AND claimed_at < NOW() - INTERVAL '5 minutes')
                  )
              AND available_at <= NOW()
              AND attempt_count < $1
            ORDER BY created_at
            FOR UPDATE SKIP LOCKED
            LIMIT $2
         )
         UPDATE compound.email_outbox AS outbox
            SET status = 'processing',
                claimed_at = NOW(),
                attempt_count = attempt_count + 1
           FROM candidates
          WHERE outbox.id = candidates.id
         RETURNING outbox.id, outbox.recipient, outbox.subject,
                   outbox.text_body, outbox.html_body, outbox.attempt_count`,
        [this.options.maxAttempts, this.options.batchSize],
      );
      await client.query("COMMIT");
      return result.rows;
    } catch (error) {
      await client.query("ROLLBACK");
      throw error;
    } finally {
      client.release();
    }
  }

  private async deliver(record: OutboxRecord): Promise<void> {
    try {
      await this.sender.send({
        to: record.recipient,
        subject: record.subject,
        text: record.text_body,
        html: record.html_body ?? undefined,
      });
      await this.database.query(
        `UPDATE compound.email_outbox
            SET status = 'delivered', delivered_at = NOW(), last_error = NULL
          WHERE id = $1 AND status = 'processing'`,
        [record.id],
      );
      emailOutboxMetrics.delivered.inc();
    } catch (error) {
      const exhausted = record.attempt_count >= this.options.maxAttempts;
      const delay = this.options.retryBaseSeconds * 2 ** (record.attempt_count - 1);
      await this.database.query(
        `UPDATE compound.email_outbox
            SET status = 'failed',
                available_at = CASE
                  WHEN $2 THEN available_at
                  ELSE NOW() + ($3 * INTERVAL '1 second')
                END,
                last_error = $4
          WHERE id = $1 AND status = 'processing'`,
        [
          record.id,
          exhausted,
          delay,
          error instanceof Error ? error.name : "EmailDeliveryError",
        ],
      );
      emailOutboxMetrics.failed.inc({ exhausted: String(exhausted) });
      logger.warn({
        event: "outbox_email_delivery_failed",
        outboxId: record.id,
        attempt: record.attempt_count,
        exhausted,
      });
    }
  }
}
