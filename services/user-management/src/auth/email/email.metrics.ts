import client from "prom-client";

const prefix = "user_management_email_outbox_";

export const emailOutboxMetrics = {
  claimed: new client.Counter({
    name: `${prefix}claimed_total`,
    help: "Email outbox messages claimed for delivery.",
  }),
  delivered: new client.Counter({
    name: `${prefix}delivered_total`,
    help: "Email outbox messages delivered successfully.",
  }),
  failed: new client.Counter({
    name: `${prefix}failed_total`,
    help: "Email outbox delivery attempts that failed.",
    labelNames: ["exhausted"] as const,
  }),
  batchDuration: new client.Histogram({
    name: `${prefix}batch_duration_seconds`,
    help: "Time spent processing an email outbox batch.",
    buckets: [0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10],
  }),
};
