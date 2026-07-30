import { logger } from "../../logger";

export interface EmailMessage {
  to: string;
  subject: string;
  text: string;
  html?: string;
}

export interface EmailSender {
  send(message: EmailMessage): Promise<void>;
}

export class DevelopmentEmailSender implements EmailSender {
  public async send(message: EmailMessage): Promise<void> {
    logger.info({
      event: "development_email_generated",
      recipient: message.to,
      subject: message.subject,
    });
  }
}

export interface HttpEmailSenderOptions {
  apiUrl: string;
  apiKey: string;
  from: string;
  timeoutMs: number;
  maxAttempts: number;
  fetcher?: typeof fetch;
}

export class EmailDeliveryError extends Error {
  public constructor(message = "Email delivery failed") {
    super(message);
    this.name = "EmailDeliveryError";
  }
}

export class HttpEmailSender implements EmailSender {
  private readonly fetcher: typeof fetch;

  public constructor(private readonly options: HttpEmailSenderOptions) {
    this.fetcher = options.fetcher ?? fetch;
    if (!options.apiUrl.startsWith("https://")) {
      throw new Error("EMAIL_API_URL must use HTTPS");
    }
  }

  public async send(message: EmailMessage): Promise<void> {
    for (let attempt = 1; attempt <= this.options.maxAttempts; attempt += 1) {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), this.options.timeoutMs);
      try {
        const response = await this.fetcher(this.options.apiUrl, {
          method: "POST",
          headers: {
            authorization: `Bearer ${this.options.apiKey}`,
            "content-type": "application/json",
          },
          body: JSON.stringify({ from: this.options.from, ...message }),
          signal: controller.signal,
        });
        if (response.ok) return;
        if (response.status < 500 && response.status !== 429) {
          throw new EmailDeliveryError();
        }
      } catch (error) {
        if (error instanceof EmailDeliveryError) throw error;
        if (attempt === this.options.maxAttempts) {
          logger.error({ event: "email_delivery_failed", attempts: attempt });
          throw new EmailDeliveryError();
        }
      } finally {
        clearTimeout(timeout);
      }
    }
    throw new EmailDeliveryError();
  }
}

export function createEmailSender(options: {
  provider: "development" | "http";
  apiUrl?: string;
  apiKey?: string;
  from: string;
  timeoutMs: number;
  maxAttempts: number;
}): EmailSender {
  if (options.provider === "development") return new DevelopmentEmailSender();
  if (!options.apiUrl || !options.apiKey) throw new Error("HTTP email provider is not configured");
  return new HttpEmailSender({
    apiUrl: options.apiUrl,
    apiKey: options.apiKey,
    from: options.from,
    timeoutMs: options.timeoutMs,
    maxAttempts: options.maxAttempts,
  });
}
