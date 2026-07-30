import { logger } from "../../logger";

export interface EmailMessage {
  to: string;
  subject: string;
  text: string;
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

export const emailSender: EmailSender = new DevelopmentEmailSender();
