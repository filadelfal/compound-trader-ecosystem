export function passwordResetEmail(token: string): {
  subject: string;
  text: string;
} {
  return {
    subject: "Reset your Compound Trader password",
    text: [
      "A password reset was requested for your Compound Trader account.",
      "",
      `Reset token: ${token}`,
      "",
      "This token expires soon and can be used only once.",
      "If you did not request this reset, you can safely ignore this email.",
    ].join("\n"),
  };
}

