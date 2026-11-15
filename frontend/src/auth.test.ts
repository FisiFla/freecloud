import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// The session logic lives in src/lib/session.ts so it can be tested without
// pulling in next-auth's server runtime.
async function loadSession() {
  vi.resetModules();
  return (await import("@/lib/session")) as typeof import("@/lib/session");
}

type JwtToken = Parameters<
  Awaited<ReturnType<typeof loadSession>>["jwtCallback"]
>[0]["token"];

describe("Auth.js jwt callback", () => {
  const origFetch = global.fetch;

  beforeEach(() => {
    vi.resetModules();
  });

  afterEach(() => {
    global.fetch = origFetch;
    delete process.env.AUTH_REFRESH_TIMEOUT_MS;
  });

  // --- #183: a transient refresh failure must not brick the session ---
  it("clears a stale refresh-error flag after a successful refresh", async () => {
    const { jwtCallback } = await loadSession();
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ access_token: "fresh-token", expires_in: 300 }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    ) as unknown as typeof fetch;

    const token = {
      sub: "u1",
      error: "RefreshAccessTokenError",
      accessToken: "stale-token",
      refreshToken: "refresh-token",
      expiresAt: Math.floor(Date.now() / 1000) - 60,
    } as unknown as JwtToken;

    const result = await jwtCallback({ token, account: null });

    expect(result.error).toBeUndefined();
    expect((result as { accessToken?: string }).accessToken).toBe("fresh-token");
  });

  it("clears a stale refresh-error flag when the token is still valid", async () => {
    const { jwtCallback } = await loadSession();
    global.fetch = vi.fn() as unknown as typeof fetch;

    const token = {
      sub: "u1",
      error: "RefreshAccessTokenError",
      accessToken: "still-valid",
      expiresAt: Math.floor(Date.now() / 1000) + 600,
    } as unknown as JwtToken;

    const result = await jwtCallback({ token, account: null });

    expect(result.error).toBeUndefined();
    expect((result as { accessToken?: string }).accessToken).toBe("still-valid");
    expect(global.fetch).not.toHaveBeenCalled();
  });

  it("records the refresh failure when Keycloak rejects the refresh token", async () => {
    const { jwtCallback } = await loadSession();
    global.fetch = vi.fn().mockResolvedValue(new Response("nope", { status: 400 })) as unknown as typeof fetch;

    const token = {
      sub: "u1",
      accessToken: "stale-token",
      refreshToken: "refresh-token",
      expiresAt: Math.floor(Date.now() / 1000) - 60,
    } as unknown as JwtToken;

    const result = await jwtCallback({ token, account: null });

    expect(result.error).toBe("RefreshAccessTokenError");
  });

  // --- #196: the refresh call is bounded by a timeout ---
  it("gives up on a hung Keycloak token endpoint instead of hanging forever", async () => {
    const { jwtCallback } = await loadSession();
    process.env.AUTH_REFRESH_TIMEOUT_MS = "50";
    global.fetch = vi.fn((_url: unknown, init: { signal: AbortSignal }) => {
      return new Promise((_resolve, reject) => {
        init.signal.addEventListener("abort", () => reject(init.signal.reason));
      });
    }) as unknown as typeof fetch;

    const token = {
      sub: "u1",
      accessToken: "stale-token",
      refreshToken: "refresh-token",
      expiresAt: Math.floor(Date.now() / 1000) - 60,
    } as unknown as JwtToken;

    const result = await jwtCallback({ token, account: null });

    expect(result.error).toBe("RefreshAccessTokenError");
  });

  it("passes an abort signal to the Keycloak token endpoint", async () => {
    const { jwtCallback } = await loadSession();
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ access_token: "fresh", expires_in: 60 }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
    global.fetch = fetchSpy as unknown as typeof fetch;

    const token = {
      sub: "u1",
      accessToken: "stale-token",
      refreshToken: "refresh-token",
      expiresAt: Math.floor(Date.now() / 1000) - 60,
    } as unknown as JwtToken;

    await jwtCallback({ token, account: null });

    const [, init] = fetchSpy.mock.calls[0];
    expect((init as RequestInit).signal).toBeInstanceOf(AbortSignal);
  });
});

describe("session lifetime", () => {
  it("configures an explicit session maxAge instead of the Auth.js 30-day default", async () => {
    const { SESSION_MAX_AGE_SECONDS } = await loadSession();
    expect(SESSION_MAX_AGE_SECONDS).toBe(8 * 60 * 60);
    // Auth.js defaults to 30 days (node_modules/@auth/core/lib/init.js).
    expect(SESSION_MAX_AGE_SECONDS).toBeLessThan(30 * 24 * 60 * 60);
  });
});

describe("production startup checks (AUTH_URL)", () => {
  const origEnv = { ...process.env };

  beforeEach(() => {
    process.env = { ...origEnv };
  });

  afterEach(() => {
    process.env = { ...origEnv };
  });

  // The check lives in @/lib/session (imported by @/auth), because @/auth
  // itself cannot be imported under vitest — next-auth's server runtime
  // requires next/server, which does not resolve outside the Next.js build.
  function validProdEnv() {
    vi.stubEnv("NODE_ENV", "production");
    delete process.env.NEXT_PHASE;
    process.env.AUTH_SECRET = "a".repeat(64);
    process.env.AUTH_KEYCLOAK_ID = "freecloud-dashboard";
    process.env.AUTH_KEYCLOAK_ISSUER = "https://auth.example.com/realms/freecloud";
    delete process.env.AUTH_URL;
    delete process.env.NEXTAUTH_URL;
  }

  it("refuses to boot in production without AUTH_URL", async () => {
    validProdEnv();
    await expect(loadSession()).rejects.toThrow("AUTH_URL");
  });

  it("boots in production with AUTH_URL set", async () => {
    validProdEnv();
    process.env.AUTH_URL = "https://app.example.com";
    const mod = await loadSession();
    expect(mod.jwtCallback).toBeDefined();
  });

  it("boots in development without AUTH_URL (Auth.js derives it from the request)", async () => {
    validProdEnv();
    vi.stubEnv("NODE_ENV", "development");
    const mod = await loadSession();
    expect(mod.jwtCallback).toBeDefined();
  });
});
