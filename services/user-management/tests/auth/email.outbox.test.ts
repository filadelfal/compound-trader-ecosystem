import type { Pool, PoolClient } from "pg";
import { EmailOutboxWorker, PostgresEmailQueue } from "../../src/auth/email/email.outbox";

describe("email outbox", () => {
  it("enqueues an email using the caller transaction", async () => {
    const query = jest.fn().mockResolvedValue({ rows: [] });
    await new PostgresEmailQueue().enqueue({ query } as unknown as PoolClient, {
      to: "user@example.com", subject: "Subject", text: "Body", html: "<p>Body</p>",
    });
    expect(query).toHaveBeenCalledWith(
      expect.stringContaining("INSERT INTO compound.email_outbox"),
      ["user@example.com", "Subject", "Body", "<p>Body</p>"],
    );
  });

  it("claims and delivers pending messages", async () => {
    const client = {
      query: jest.fn()
        .mockResolvedValueOnce({})
        .mockResolvedValueOnce({ rows: [{
          id: "outbox-1", recipient: "user@example.com", subject: "Subject",
          text_body: "Body", html_body: null, attempt_count: 1,
        }] })
        .mockResolvedValueOnce({}),
      release: jest.fn(),
    };
    const query = jest.fn().mockResolvedValue({});
    const database = { connect: jest.fn().mockResolvedValue(client), query } as unknown as Pool;
    const sender = { send: jest.fn().mockResolvedValue(undefined) };
    const worker = new EmailOutboxWorker(database, sender, {
      pollIntervalMs: 1000, batchSize: 10, maxAttempts: 3, retryBaseSeconds: 5,
    });

    await expect(worker.processBatch()).resolves.toBe(1);
    expect(sender.send).toHaveBeenCalledWith({
      to: "user@example.com", subject: "Subject", text: "Body", html: undefined,
    });
    expect(query).toHaveBeenCalledWith(
      expect.stringContaining("status = 'delivered'"), ["outbox-1"],
    );
  });

  it("reschedules transient failures without storing message contents in errors", async () => {
    const client = {
      query: jest.fn()
        .mockResolvedValueOnce({})
        .mockResolvedValueOnce({ rows: [{
          id: "outbox-2", recipient: "user@example.com", subject: "Subject",
          text_body: "secret token", html_body: null, attempt_count: 2,
        }] })
        .mockResolvedValueOnce({}),
      release: jest.fn(),
    };
    const query = jest.fn().mockResolvedValue({});
    const database = { connect: jest.fn().mockResolvedValue(client), query } as unknown as Pool;
    const worker = new EmailOutboxWorker(
      database,
      { send: jest.fn().mockRejectedValue(new Error("provider unavailable")) },
      { pollIntervalMs: 1000, batchSize: 10, maxAttempts: 3, retryBaseSeconds: 5 },
    );

    await expect(worker.processBatch()).resolves.toBe(1);
    expect(query).toHaveBeenCalledWith(
      expect.stringContaining("status = 'failed'"), ["outbox-2", false, 10, "Error"],
    );
    expect(JSON.stringify(query.mock.calls)).not.toContain("secret token");
  });
});
