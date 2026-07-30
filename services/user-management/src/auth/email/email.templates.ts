function escapeHtml(value: string): string {
  return value.replace(/[&<>"']/g, (character) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  })[character] ?? character);
}

export function verificationEmail(token: string, webAppUrl: string): {
  subject: string; text: string; html: string;
} {
  const link = `${webAppUrl.replace(/\/$/, "")}/verify-email?token=${encodeURIComponent(token)}`;
  return {
    subject: "Verify your Compound Trader email",
    text: `Verify your email using this one-time link:\n\n${link}\n\nIf you did not create this account, ignore this email.`,
    html: `<p>Verify your email using this one-time link:</p><p><a href="${escapeHtml(link)}">Verify email</a></p><p>If you did not create this account, ignore this email.</p>`,
  };
}

export function passwordResetEmail(token: string, webAppUrl: string): {
  subject: string;
  text: string;
  html: string;
} {
  const link = `${webAppUrl.replace(/\/$/, "")}/reset-password?token=${encodeURIComponent(token)}`;
  return {
    subject: "Reset your Compound Trader password",
    text: [
      "A password reset was requested for your Compound Trader account.",
      "",
      `Reset link: ${link}`,
      "",
      "This token expires soon and can be used only once.",
      "If you did not request this reset, you can safely ignore this email.",
    ].join("\n"),
    html: `<p>A password reset was requested for your Compound Trader account.</p><p><a href="${escapeHtml(link)}">Reset password</a></p><p>This link expires soon and can be used only once. If you did not request it, ignore this email.</p>`,
  };
}
