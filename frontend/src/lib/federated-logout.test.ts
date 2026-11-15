import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { PENDING_SIGNIN_ERROR_KEY } from "./signin-error";

const { signOutMock } = vi.hoisted(() => ({ signOutMock: vi.fn() }));
vi.mock("next-auth/react", () => ({ signOut: signOutMock }));

import { federatedSignOut } from "./federated-logout";

describe("federatedSignOut (lib/federated-logout)", () => {
  const origFetch = global.fetch;
  const origLocation = window.location;
  const assignMock = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    window.sessionStorage.clear();
    Object.defineProperty(window, "location", {
      configurable: true,
      writable: true,
      value: { origin: "http://localhost", assign: assignMock },
    });
  });

  afterEach(() => {
    global.fetch = origFetch;
    window.sessionStorage.clear();
    Object.defineProperty(window, "location", {
      configurable: true,
      writable: true,
      value: origLocation,
    });
  });

  function okLogout(logoutUrl: string) {
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ logoutUrl }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    ) as unknown as typeof fetch;
  }

  it("POSTs to the federated-logout route and navigates to the Keycloak logout URL", async () => {
    const target = "https://auth.example.com/realms/freecloud/protocol/openid-connect/logout?id_token_hint=abc";
    okLogout(target);
    await federatedSignOut();

    const fetchMock = vi.mocked(global.fetch);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/auth/federated-logout");
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("POST");
    expect(assignMock).toHaveBeenCalledWith(target);
    expect(signOutMock).not.toHaveBeenCalled();
    expect(window.sessionStorage.getItem(PENDING_SIGNIN_ERROR_KEY)).toBeNull();
  });

  it("navigates to the relative sign-in fallback URL", async () => {
    okLogout("/signin");
    await federatedSignOut();
    expect(assignMock).toHaveBeenCalledWith("/signin");
    expect(signOutMock).not.toHaveBeenCalled();
  });

  it("falls back to local-only sign-out when the POST fails", async () => {
    global.fetch = vi.fn().mockRejectedValue(new Error("network down")) as unknown as typeof fetch;
    await federatedSignOut();
    expect(assignMock).not.toHaveBeenCalled();
    expect(signOutMock).toHaveBeenCalledWith({ callbackUrl: "/signin" });
  });

  it("falls back to local-only sign-out when the route answers an error", async () => {
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: "nope" }), { status: 500 }),
    ) as unknown as typeof fetch;
    await federatedSignOut();
    expect(assignMock).not.toHaveBeenCalled();
    expect(signOutMock).toHaveBeenCalledWith({ callbackUrl: "/signin" });
  });

  it("falls back to local-only sign-out when the logout URL is missing or not http(s)", async () => {
    okLogout("javascript:alert(document.domain)");
    await federatedSignOut();
    expect(assignMock).not.toHaveBeenCalled();
    expect(signOutMock).toHaveBeenCalledWith({ callbackUrl: "/signin" });

    vi.clearAllMocks();
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({}), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    ) as unknown as typeof fetch;
    await federatedSignOut();
    expect(assignMock).not.toHaveBeenCalled();
    expect(signOutMock).toHaveBeenCalledWith({ callbackUrl: "/signin" });
  });

  it("stashes the session_expired indication for the sign-in page when expiring", async () => {
    okLogout("https://auth.example.com/realms/freecloud/protocol/openid-connect/logout?id_token_hint=abc");
    await federatedSignOut({ expired: true });
    expect(window.sessionStorage.getItem(PENDING_SIGNIN_ERROR_KEY)).toBe("session_expired");
    expect(assignMock).toHaveBeenCalledTimes(1);
  });

  it("keeps the session_expired indication on the local-only fallback path", async () => {
    global.fetch = vi.fn().mockRejectedValue(new Error("network down")) as unknown as typeof fetch;
    await federatedSignOut({ expired: true });
    expect(window.sessionStorage.getItem(PENDING_SIGNIN_ERROR_KEY)).toBe("session_expired");
    expect(signOutMock).toHaveBeenCalledWith({ callbackUrl: "/signin?error=session_expired" });
  });
});
