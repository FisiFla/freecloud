import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { NextRequest } from "next/server";

const { getTokenMock, signOutMock } = vi.hoisted(() => ({
  getTokenMock: vi.fn(),
  signOutMock: vi.fn(),
}));
// @/auth pulls next-auth's server runtime (next/server), which does not
// resolve under vitest.
vi.mock("@/auth", () => ({ signOut: signOutMock }));
vi.mock("next-auth/jwt", () => ({ getToken: getTokenMock }));

import { GET, POST } from "./route";
import { keycloakIssuer } from "@/lib/keycloak";

function makePost(url = "http://localhost/api/auth/federated-logout", init?: RequestInit): NextRequest {
  return new NextRequest(new Request(url, { method: "POST", ...init }));
}

function discoveryResponse(endSessionEndpoint: string): Response {
  return new Response(JSON.stringify({ end_session_endpoint: endSessionEndpoint }), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
}

describe("federated logout route (api/auth/federated-logout)", () => {
  const origFetch = global.fetch;
  const origAuthUrl = process.env.AUTH_URL;

  beforeEach(() => {
    vi.clearAllMocks();
    process.env.AUTH_URL = "https://app.example.com";
  });

  afterEach(() => {
    global.fetch = origFetch;
    if (origAuthUrl === undefined) delete process.env.AUTH_URL;
    else process.env.AUTH_URL = origAuthUrl;
  });

  it("sends the browser to the issuer end_session_endpoint with id_token_hint and a fixed post_logout_redirect_uri", async () => {
    getTokenMock.mockResolvedValue({ idToken: "id-token-123" });
    const discovered = `${keycloakIssuer}/protocol/openid-connect/logout`;
    global.fetch = vi.fn().mockResolvedValue(
      discoveryResponse(discovered),
    ) as unknown as typeof fetch;

    const res = await POST(makePost());
    expect(res.status).toBe(200);
    const body = (await res.json()) as { logoutUrl: string };
    const target = new URL(body.logoutUrl);
    expect(`${target.origin}${target.pathname}`).toBe(discovered);
    expect(target.searchParams.get("id_token_hint")).toBe("id-token-123");
    expect(target.searchParams.get("post_logout_redirect_uri")).toBe("https://app.example.com/signin");
    // The local Auth.js session is cleared as part of the same call.
    expect(signOutMock).toHaveBeenCalledWith({ redirect: false });
  });

  it("discovers the end_session_endpoint over the server-side Keycloak fetch", async () => {
    getTokenMock.mockResolvedValue({ idToken: "id-token-123" });
    const fetchSpy = vi.fn().mockResolvedValue(
      discoveryResponse("http://localhost:8081/realms/freecloud/protocol/openid-connect/logout"),
    );
    global.fetch = fetchSpy as unknown as typeof fetch;

    await POST(makePost());
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(String(fetchSpy.mock.calls[0][0])).toBe(
      `${keycloakIssuer}/.well-known/openid-configuration`,
    );
  });

  it("ignores request-controlled input when building the post_logout_redirect_uri (no open redirect)", async () => {
    getTokenMock.mockResolvedValue({ idToken: "id-token-123" });
    global.fetch = vi.fn().mockResolvedValue(
      discoveryResponse(`${keycloakIssuer}/protocol/openid-connect/logout`),
    ) as unknown as typeof fetch;

    const res = await POST(
      makePost("http://localhost/api/auth/federated-logout?next=https://evil.example.com&callbackUrl=https://evil.example.com", {
        headers: { "x-forwarded-host": "evil.example.com" },
      }),
    );
    const body = (await res.json()) as { logoutUrl: string };
    expect(body.logoutUrl).not.toContain("evil.example.com");
    expect(new URL(body.logoutUrl).searchParams.get("post_logout_redirect_uri")).toBe(
      "https://app.example.com/signin",
    );
  });

  it("clears the local session and falls back to the plain sign-in page when no id token is available", async () => {
    getTokenMock.mockResolvedValue(null);
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;

    const res = await POST(makePost());
    expect(res.status).toBe(200);
    await expect(res.json()).resolves.toEqual({ logoutUrl: "/signin" });
    expect(signOutMock).toHaveBeenCalledWith({ redirect: false });
    // No id token — no point contacting Keycloak.
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("falls back to the sign-in page when the session has no idToken field", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "at" });
    global.fetch = vi.fn() as unknown as typeof fetch;

    const res = await POST(makePost());
    await expect(res.json()).resolves.toEqual({ logoutUrl: "/signin" });
    expect(signOutMock).toHaveBeenCalledWith({ redirect: false });
  });

  it("falls back to the sign-in page when the session cookie is unreadable", async () => {
    getTokenMock.mockRejectedValue(new Error("bad cookie"));
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;

    const res = await POST(makePost());
    expect(res.status).toBe(200);
    await expect(res.json()).resolves.toEqual({ logoutUrl: "/signin" });
    expect(signOutMock).toHaveBeenCalledWith({ redirect: false });
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects a cross-origin POST with 403 without touching the session", async () => {
    getTokenMock.mockResolvedValue({ idToken: "id-token-123" });
    global.fetch = vi.fn() as unknown as typeof fetch;

    const res = await POST(
      makePost("http://localhost/api/auth/federated-logout", {
        headers: { origin: "https://evil.example.com" },
      }),
    );
    expect(res.status).toBe(403);
    expect(signOutMock).not.toHaveBeenCalled();
    expect(getTokenMock).not.toHaveBeenCalled();
  });

  it("rejects a POST with Sec-Fetch-Site: cross-site with 403", async () => {
    const res = await POST(
      makePost("http://localhost/api/auth/federated-logout", {
        headers: { "sec-fetch-site": "cross-site" },
      }),
    );
    expect(res.status).toBe(403);
    expect(signOutMock).not.toHaveBeenCalled();
  });

  it("rejects GET with 405 without touching the session (logout is POST-only)", async () => {
    const res = await GET(new NextRequest(new Request("http://localhost/api/auth/federated-logout")));
    expect(res.status).toBe(405);
    expect(signOutMock).not.toHaveBeenCalled();
    expect(getTokenMock).not.toHaveBeenCalled();
  });

  it("falls back to the constructed logout URL when discovery fails, so the Keycloak session still ends", async () => {
    getTokenMock.mockResolvedValue({ idToken: "id-token-123" });
    global.fetch = vi.fn().mockRejectedValue(new Error("ECONNREFUSED")) as unknown as typeof fetch;

    const res = await POST(makePost());
    expect(res.status).toBe(200);
    const body = (await res.json()) as { logoutUrl: string };
    const target = new URL(body.logoutUrl);
    expect(`${target.origin}${target.pathname}`).toBe(
      `${keycloakIssuer}/protocol/openid-connect/logout`,
    );
    expect(target.searchParams.get("id_token_hint")).toBe("id-token-123");
    expect(target.searchParams.get("post_logout_redirect_uri")).toBe("https://app.example.com/signin");
  });

  it("ignores a discovered end_session_endpoint on a foreign origin", async () => {
    getTokenMock.mockResolvedValue({ idToken: "id-token-123" });
    global.fetch = vi.fn().mockResolvedValue(
      discoveryResponse("https://evil.example.com/logout"),
    ) as unknown as typeof fetch;

    const res = await POST(makePost());
    const body = (await res.json()) as { logoutUrl: string };
    expect(body.logoutUrl).not.toContain("evil.example.com");
    expect(new URL(body.logoutUrl).origin).toBe(new URL(keycloakIssuer).origin);
  });

  it("omits post_logout_redirect_uri when AUTH_URL is not configured, but still ends the Keycloak session", async () => {
    delete process.env.AUTH_URL;
    getTokenMock.mockResolvedValue({ idToken: "id-token-123" });
    global.fetch = vi.fn().mockResolvedValue(
      discoveryResponse(`${keycloakIssuer}/protocol/openid-connect/logout`),
    ) as unknown as typeof fetch;

    const res = await POST(makePost());
    expect(res.status).toBe(200);
    const body = (await res.json()) as { logoutUrl: string };
    const target = new URL(body.logoutUrl);
    expect(target.searchParams.get("id_token_hint")).toBe("id-token-123");
    expect(target.searchParams.get("post_logout_redirect_uri")).toBeNull();
  });
});
