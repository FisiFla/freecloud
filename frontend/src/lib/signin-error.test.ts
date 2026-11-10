import { describe, it, expect } from "vitest";
import { signInErrorMessage } from "./signin-error";

describe("signInErrorMessage", () => {
  it("explains an expired session", () => {
    expect(signInErrorMessage("session_expired")).toBe(
      "Your session has expired. Sign in again to continue.",
    );
  });

  it("explains an Auth.js failure without blaming the user", () => {
    expect(signInErrorMessage("auth_failed")).toBe(
      "Sign-in failed. Try again — if this keeps happening, the identity provider may be unreachable; contact your administrator.",
    );
  });

  it("falls back to a generic message for unknown errors and never echoes the raw value", () => {
    const message = signInErrorMessage("something_unexpected");
    expect(message).toBe("Sign-in failed. Please try again.");
    expect(message).not.toContain("something_unexpected");
  });

  it("returns null when there is no error", () => {
    expect(signInErrorMessage(null)).toBeNull();
    expect(signInErrorMessage(undefined)).toBeNull();
    expect(signInErrorMessage("")).toBeNull();
  });
});
