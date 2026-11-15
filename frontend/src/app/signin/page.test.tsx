import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";

const { searchParamsMock } = vi.hoisted(() => ({ searchParamsMock: vi.fn() }));

vi.mock("next/navigation", () => ({ useSearchParams: searchParamsMock }));
vi.mock("next-auth/react", () => ({ signIn: vi.fn() }));

import SignIn from "./page";
import { PENDING_SIGNIN_ERROR_KEY } from "@/lib/signin-error";

describe("SignIn page error surfacing", () => {
  beforeEach(() => {
    searchParamsMock.mockReset();
  });

  function setErrorParam(value: string | null) {
    const params = new URLSearchParams();
    if (value !== null) params.set("error", value);
    searchParamsMock.mockReturnValue(params);
  }

  it("shows the session-expired message as an alert above the sign-in button", () => {
    setErrorParam("session_expired");
    render(<SignIn />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Your session has expired. Sign in again to continue.");
    expect(screen.getByRole("button", { name: "Sign in with Keycloak" })).toBeInTheDocument();
  });

  it("shows the auth-failed message as an alert", () => {
    setErrorParam("auth_failed");
    render(<SignIn />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Sign-in failed. Try again — if this keeps happening, the identity provider may be unreachable; contact your administrator.",
    );
  });

  it("shows a generic message for unknown errors and never echoes the raw param", () => {
    setErrorParam("<script>alert(1)</script>");
    render(<SignIn />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Sign-in failed. Please try again.");
    expect(alert.textContent).not.toContain("<script>");
  });

  it("renders no alert when no error param is present", () => {
    setErrorParam(null);
    render(<SignIn />);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows the pending expired hint left by federated sign-out, exactly once", () => {
    setErrorParam(null);
    window.sessionStorage.setItem(PENDING_SIGNIN_ERROR_KEY, "session_expired");
    render(<SignIn />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Your session has expired. Sign in again to continue.",
    );
    // Consumed: a reload must not show it again.
    expect(window.sessionStorage.getItem(PENDING_SIGNIN_ERROR_KEY)).toBeNull();
  });

  it("prefers the error query param over the pending stash", () => {
    setErrorParam("auth_failed");
    window.sessionStorage.setItem(PENDING_SIGNIN_ERROR_KEY, "session_expired");
    render(<SignIn />);
    expect(screen.getByRole("alert")).toHaveTextContent(/Sign-in failed\. Try again/);
  });
});
