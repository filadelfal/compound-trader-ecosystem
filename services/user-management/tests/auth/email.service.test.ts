import {
  EmailDeliveryError,
  HttpEmailSender,
} from "../../src/auth/email/email.service";

describe("HTTP email sender", () => {
  const message = {
    to: "user@example.com",
    subject: "Test",
    text: "Test message",
  };

  it("sends the provider request without exposing credentials in the body", async () => {
    const fetcher = jest.fn().mockResolvedValue({ ok: true, status: 202 });
    const sender = new HttpEmailSender({
      apiUrl: "https://mail.example.com/send",
      apiKey: "secret-provider-key",
      from: "no-reply@example.com",
      timeoutMs: 1000,
      maxAttempts: 2,
      fetcher,
    });

    await sender.send(message);

    expect(fetcher).toHaveBeenCalledTimes(1);
    const [, request] = fetcher.mock.calls[0];
    expect(request.headers.authorization).toBe("Bearer secret-provider-key");
    expect(request.body).not.toContain("secret-provider-key");
  });

  it("retries transient provider failures", async () => {
    const fetcher = jest.fn()
      .mockResolvedValueOnce({ ok: false, status: 503 })
      .mockResolvedValueOnce({ ok: true, status: 202 });
    const sender = new HttpEmailSender({
      apiUrl: "https://mail.example.com/send",
      apiKey: "secret-provider-key",
      from: "no-reply@example.com",
      timeoutMs: 1000,
      maxAttempts: 2,
      fetcher,
    });

    await sender.send(message);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("does not retry permanent provider rejection", async () => {
    const fetcher = jest.fn().mockResolvedValue({ ok: false, status: 400 });
    const sender = new HttpEmailSender({
      apiUrl: "https://mail.example.com/send",
      apiKey: "secret-provider-key",
      from: "no-reply@example.com",
      timeoutMs: 1000,
      maxAttempts: 3,
      fetcher,
    });

    await expect(sender.send(message)).rejects.toBeInstanceOf(EmailDeliveryError);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("rejects insecure provider endpoints", () => {
    expect(() => new HttpEmailSender({
      apiUrl: "http://mail.example.com/send",
      apiKey: "secret-provider-key",
      from: "no-reply@example.com",
      timeoutMs: 1000,
      maxAttempts: 1,
    })).toThrow("must use HTTPS");
  });
});
