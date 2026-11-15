import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { createKeycloakFetch, refreshAccessToken } from "./keycloak";

// Split-horizon Keycloak fetch: the browser-facing issuer
// (http://localhost:8081/realms/freecloud on the localhost dev stack)
// is unreachable from inside the frontend container, so server-side
// OIDC calls must be rewritten to the internal origin
// (http://keycloak:8080). Only exact issuer-origin matches may be
// rewritten — anything else passes through untouched.
describe("createKeycloakFetch", () => {
  const issuer = "http://localhost:8081/realms/freecloud";
  const internal = "http://keycloak:8080";

  function stubFetch() {
    return vi.fn<typeof fetch>(async () => new Response("{}", { status: 200 }));
  }

  it("returns undefined when no internal URL is configured", () => {
    expect(createKeycloakFetch(issuer, undefined, stubFetch())).toBeUndefined();
    expect(createKeycloakFetch(issuer, "", stubFetch())).toBeUndefined();
  });

  it("returns undefined for malformed issuer or internal URLs", () => {
    expect(createKeycloakFetch("not-a-url", internal, stubFetch())).toBeUndefined();
    expect(createKeycloakFetch(issuer, "not-a-url", stubFetch())).toBeUndefined();
  });

  it("rewrites issuer-origin URLs to the internal origin, preserving path and query", async () => {
    const base = stubFetch();
    const f = createKeycloakFetch(issuer, internal, base)!;
    await f("http://localhost:8081/realms/freecloud/protocol/openid-connect/token?x=1", {
      method: "POST",
    });
    expect(base).toHaveBeenCalledWith(
      "http://keycloak:8080/realms/freecloud/protocol/openid-connect/token?x=1",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("accepts URL objects", async () => {
    const base = stubFetch();
    const f = createKeycloakFetch(issuer, internal, base)!;
    await f(new URL("http://localhost:8081/realms/freecloud/.well-known/openid-configuration"));
    const arg = base.mock.calls[0][0] as URL;
    expect(arg.toString()).toBe(
      "http://keycloak:8080/realms/freecloud/.well-known/openid-configuration",
    );
  });

  it("accepts Request objects and preserves the method", async () => {
    const base = stubFetch();
    const f = createKeycloakFetch(issuer, internal, base)!;
    await f(
      new Request("http://localhost:8081/realms/freecloud/protocol/openid-connect/userinfo", {
        method: "GET",
      }),
    );
    const arg = base.mock.calls[0][0] as Request;
    expect(arg.url).toBe("http://keycloak:8080/realms/freecloud/protocol/openid-connect/userinfo");
    expect(arg.method).toBe("GET");
  });

  it("passes foreign origins through untouched", async () => {
    const base = stubFetch();
    const f = createKeycloakFetch(issuer, internal, base)!;
    await f("https://example.com/realms/freecloud/protocol/openid-connect/token");
    expect(base).toHaveBeenCalledWith(
      "https://example.com/realms/freecloud/protocol/openid-connect/token",
      undefined,
    );
  });

  it("does not rewrite a different port on the issuer host", async () => {
    const base = stubFetch();
    const f = createKeycloakFetch(issuer, internal, base)!;
    await f("http://localhost:9999/realms/freecloud/protocol/openid-connect/token");
    expect(base).toHaveBeenCalledWith(
      "http://localhost:9999/realms/freecloud/protocol/openid-connect/token",
      undefined,
    );
  });
});

describe("production issuer validation (module wiring)", () => {
  const origEnv = { ...process.env };

  beforeEach(() => {
    process.env = { ...origEnv };
  });

  afterEach(() => {
    process.env = { ...origEnv };
  });

  async function loadKeycloak() {
    vi.resetModules();
    return await import("./keycloak");
  }

  it("refuses to boot in production with an http issuer", async () => {
    vi.stubEnv("NODE_ENV", "production");
    process.env.AUTH_KEYCLOAK_ID = "freecloud-dashboard";
    process.env.AUTH_KEYCLOAK_ISSUER = "http://keycloak:8080/realms/freecloud";
    await expect(loadKeycloak()).rejects.toThrow(/https/);
  });

  it("boots in production with an https issuer on a public host", async () => {
    vi.stubEnv("NODE_ENV", "production");
    process.env.AUTH_KEYCLOAK_ID = "freecloud-dashboard";
    process.env.AUTH_KEYCLOAK_ISSUER = "https://auth.example.com/realms/freecloud";
    const mod = await loadKeycloak();
    expect(mod.keycloakIssuer).toBe("https://auth.example.com/realms/freecloud");
  });

  it("boots in production with an https loopback issuer (warns; prod-smoke uses https://*.localhost)", async () => {
    vi.stubEnv("NODE_ENV", "production");
    process.env.AUTH_KEYCLOAK_ID = "freecloud-dashboard";
    process.env.AUTH_KEYCLOAK_ISSUER = "https://auth.localhost/realms/freecloud";
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      const mod = await loadKeycloak();
      expect(mod.keycloakIssuer).toBe("https://auth.localhost/realms/freecloud");
      expect(errorSpy).toHaveBeenCalled();
    } finally {
      errorSpy.mockRestore();
    }
  });
});

describe("refreshAccessToken request coalescing", () => {
  const origFetch = global.fetch;

  afterEach(() => {
    global.fetch = origFetch;
  });

  function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((r) => {
      resolve = r;
    });
    return { promise, resolve };
  }

  function tokenResponse() {
    return new Response(
      JSON.stringify({ access_token: "fresh-at", refresh_token: "fresh-rt", expires_in: 300 }),
      { status: 200, headers: { "content-type": "application/json" } },
    );
  }

  it("flags an error without calling Keycloak when there is no refresh token", async () => {
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;
    const out = await refreshAccessToken({ accessToken: "stale" });
    expect(out.error).toBe("RefreshAccessTokenError");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("shares one in-flight refresh between concurrent callers with the same refresh token", async () => {
    const gate = deferred<Response>();
    const fetchSpy = vi.fn().mockReturnValue(gate.promise);
    global.fetch = fetchSpy as unknown as typeof fetch;

    const p1 = refreshAccessToken({ refreshToken: "same-rt", accessToken: "stale" });
    const p2 = refreshAccessToken({ refreshToken: "same-rt", accessToken: "stale" });
    gate.resolve(tokenResponse());
    const [r1, r2] = await Promise.all([p1, p2]);

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(r1.accessToken).toBe("fresh-at");
    expect(r2.accessToken).toBe("fresh-at");
    expect(r1.refreshToken).toBe("fresh-rt");
    // Each waiter gets its own copy — mutating one must not affect the other.
    expect(r1).not.toBe(r2);
    r1.accessToken = "mutated";
    expect(r2.accessToken).toBe("fresh-at");
  });

  it("does not coalesce refreshes for different refresh tokens", async () => {
    const gate = deferred<Response>();
    const fetchSpy = vi.fn().mockReturnValue(gate.promise);
    global.fetch = fetchSpy as unknown as typeof fetch;

    const p1 = refreshAccessToken({ refreshToken: "rt-one" });
    const p2 = refreshAccessToken({ refreshToken: "rt-two" });
    gate.resolve(tokenResponse());
    await Promise.all([p1, p2]);

    expect(fetchSpy).toHaveBeenCalledTimes(2);
  });

  it("answers a spent refresh token from the first exchange during the grace period", async () => {
    vi.useFakeTimers();
    try {
      const fetchSpy = vi.fn().mockResolvedValue(tokenResponse());
      global.fetch = fetchSpy as unknown as typeof fetch;

      const first = await refreshAccessToken({ refreshToken: "spent-rt" });
      // A request that still carries the spent token must not present it to
      // Keycloak again (strict rotation would revoke the session).
      const late = await refreshAccessToken({ refreshToken: "spent-rt" });
      expect(fetchSpy).toHaveBeenCalledTimes(1);
      expect(late.accessToken).toBe(first.accessToken);
      expect(late.refreshToken).toBe("fresh-rt");

      // After the grace period the entry is gone.
      vi.advanceTimersByTime(31_000);
      await refreshAccessToken({ refreshToken: "spent-rt" });
      expect(fetchSpy).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("refreshes again when Keycloak did not rotate the refresh token", async () => {
    const fetchSpy = vi.fn().mockImplementation(
      async () =>
        new Response(JSON.stringify({ access_token: "fresh-at", expires_in: 300 }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
    );
    global.fetch = fetchSpy as unknown as typeof fetch;

    await refreshAccessToken({ refreshToken: "static-rt" });
    await refreshAccessToken({ refreshToken: "static-rt" });

    expect(fetchSpy).toHaveBeenCalledTimes(2);
  });

  it("propagates a refresh failure to all waiters and retries afterwards", async () => {
    const gate = deferred<Response>();
    const fetchSpy = vi.fn().mockReturnValue(gate.promise);
    global.fetch = fetchSpy as unknown as typeof fetch;

    const p1 = refreshAccessToken({ refreshToken: "rt" });
    const p2 = refreshAccessToken({ refreshToken: "rt" });
    gate.resolve(new Response("invalid_grant", { status: 400 }));
    const [r1, r2] = await Promise.all([p1, p2]);

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(r1.error).toBe("RefreshAccessTokenError");
    expect(r2.error).toBe("RefreshAccessTokenError");

    // The failed entry is evicted — the next call tries Keycloak again.
    fetchSpy.mockResolvedValue(tokenResponse());
    const r3 = await refreshAccessToken({ refreshToken: "rt" });
    expect(fetchSpy).toHaveBeenCalledTimes(2);
    expect(r3.accessToken).toBe("fresh-at");
    expect(r3.error).toBeUndefined();
  });
});
