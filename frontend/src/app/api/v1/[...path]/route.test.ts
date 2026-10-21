import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { NextRequest } from "next/server";

// M6 regression coverage for the BFF proxy: before this fix, the raw
// Keycloak access token was copied onto the client-visible NextAuth session
// and attached to the Go backend directly from browser JS. Now every
// request from lib/api.ts hits this same-origin Route Handler, which
// resolves the token SERVER-SIDE via next-auth/jwt's getToken() (mocked
// below) and attaches it itself — the browser never holds it.
const { getTokenMock } = vi.hoisted(() => ({ getTokenMock: vi.fn() }));
vi.mock("next-auth/jwt", () => ({ getToken: getTokenMock }));

import { GET, POST, readBoundedBody } from "./route";

function makeRequest(url: string, init?: RequestInit): NextRequest {
  return new NextRequest(new Request(url, init));
}

/** Builds a request with a streaming (chunked) body — no Content-Length. */
function makeStreamingRequest(url: string, body: ReadableStream<Uint8Array>): NextRequest {
  return new NextRequest(
    new Request(url, {
      method: "POST",
      body,
      // @ts-expect-error -- undici-only option, required for a stream body.
      duplex: "half",
    }),
  );
}

describe("BFF proxy route (app/api/v1/[...path])", () => {
  const origFetch = global.fetch;

  beforeEach(() => {
    getTokenMock.mockReset();
  });

  afterEach(() => {
    global.fetch = origFetch;
  });

  it("rejects an unauthenticated request to a protected path with 401 and never calls the backend", async () => {
    getTokenMock.mockResolvedValue(null);
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/users");
    const res = await GET(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(401);
    const body = await res.json();
    expect(body.success).toBe(false);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("attaches the server-resolved Bearer token to the backend request for an authenticated call, without the browser ever supplying it", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "real-keycloak-access-token" });
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ success: true, data: [] }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
    global.fetch = fetchSpy as unknown as typeof fetch;

    // Note: no Authorization header on the INCOMING browser request at all —
    // that's the whole point of the fix.
    const req = makeRequest("http://localhost/api/v1/users");
    const res = await GET(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(200);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const [calledUrl, calledInit] = fetchSpy.mock.calls[0];
    expect(String(calledUrl)).toBe("http://localhost:8080/api/v1/users");
    const headers = calledInit.headers as Headers;
    expect(headers.get("Authorization")).toBe("Bearer real-keycloak-access-token");
  });

  it("does not forward a request when the refresh token flow has failed", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "stale-token", error: "RefreshAccessTokenError" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/users");
    const res = await GET(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  // --- #183: a stale refresh-error flag must not reject a valid token ---
  it("forwards a still-valid access token even when the stored refresh flag is set", async () => {
    getTokenMock.mockResolvedValue({
      accessToken: "still-valid",
      error: "RefreshAccessTokenError",
      expiresAt: Math.floor(Date.now() / 1000) + 600,
    });
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ success: true, data: [] }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
    global.fetch = fetchSpy as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/users");
    const res = await GET(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(200);
    const [, calledInit] = fetchSpy.mock.calls[0];
    const headers = calledInit.headers as Headers;
    expect(headers.get("Authorization")).toBe("Bearer still-valid");
  });

  it("forwards a public backend path (forgot-password) even with no session", async () => {
    getTokenMock.mockResolvedValue(null);
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ success: true, data: { message: "ok" } }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
    global.fetch = fetchSpy as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/auth/forgot-password", {
      method: "POST",
      body: JSON.stringify({ email: "user@example.com" }),
      headers: { "content-type": "application/json" },
    });
    const res = await POST(req, { params: Promise.resolve({ path: ["auth", "forgot-password"] }) });

    expect(res.status).toBe(200);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const [calledUrl, calledInit] = fetchSpy.mock.calls[0];
    expect(String(calledUrl)).toBe("http://localhost:8080/api/v1/auth/forgot-password");
    const headers = calledInit.headers as Headers;
    expect(headers.get("Authorization")).toBeNull();
  });

  it("forwards the X-Org-Id header from the browser request", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn().mockResolvedValue(new Response("{}", { status: 200 }));
    global.fetch = fetchSpy as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/apps", {
      headers: { "x-org-id": "org-123" },
    });
    await GET(req, { params: Promise.resolve({ path: ["apps"] }) });

    const [, calledInit] = fetchSpy.mock.calls[0];
    const headers = calledInit.headers as Headers;
    expect(headers.get("X-Org-Id")).toBe("org-123");
  });

  it("returns 502 when the backend is unreachable", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    global.fetch = vi.fn().mockRejectedValue(new Error("ECONNREFUSED")) as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/users");
    const res = await GET(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(502);
  });

  it("rejects path traversal segments that would escape /api/v1", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/../metrics");
    const res = await GET(req, { params: Promise.resolve({ path: ["..", "metrics"] }) });

    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects double-encoded path traversal segments", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;

    // %2e%2e decodes to ".." — sanitizePathParts must reject after decode.
    const res = await GET(makeRequest("http://localhost/api/v1/%2e%2e/metrics"), {
      params: Promise.resolve({ path: ["%2e%2e", "metrics"] }),
    });
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects empty path segments after decode", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;
    const res = await GET(makeRequest("http://localhost/api/v1//users"), {
      params: Promise.resolve({ path: ["", "users"] }),
    });
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects encoded slash path segments after decode", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;
    // %2F decodes to "/" — sanitizePathParts must reject multi-segment smuggling.
    const res = await GET(makeRequest("http://localhost/api/v1/foo%2Fbar"), {
      params: Promise.resolve({ path: ["foo%2Fbar"] }),
    });
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects encoded backslash path segments after decode", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;
    const res = await GET(makeRequest("http://localhost/api/v1/foo%5cbar"), {
      params: Promise.resolve({ path: ["foo%5cbar"] }),
    });
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects overlong path segments after decode", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;
    const longSeg = "a".repeat(257);
    const res = await GET(makeRequest(`http://localhost/api/v1/${longSeg}`), {
      params: Promise.resolve({ path: [longSeg] }),
    });
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects too many path segments", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;
    const many = Array.from({ length: 17 }, (_, i) => `s${i}`);
    const res = await GET(makeRequest("http://localhost/api/v1/" + many.join("/")), {
      params: Promise.resolve({ path: many }),
    });
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects path segments containing control characters", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;
    const res = await GET(makeRequest("http://localhost/api/v1/users"), {
      params: Promise.resolve({ path: ["bad\u0001seg"] }),
    });
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects path segments with leading or trailing whitespace", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;
    const res = await GET(makeRequest("http://localhost/api/v1/users"), {
      params: Promise.resolve({ path: [" users"] }),
    });
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects triple-dot path segments", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;
    const res = await GET(makeRequest("http://localhost/api/v1/.../users"), {
      params: Promise.resolve({ path: ["...", "users"] }),
    });
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("forwards Content-Disposition so browser downloads work through the BFF", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    global.fetch = vi.fn().mockResolvedValue(
      new Response("a,b,c\n", {
        status: 200,
        headers: {
          "content-type": "text/csv",
          "content-disposition": 'attachment; filename="audit.csv"',
        },
      }),
    ) as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/audit-logs/export?format=csv");
    const res = await GET(req, {
      params: Promise.resolve({ path: ["audit-logs", "export"] }),
    });

    expect(res.status).toBe(200);
    expect(res.headers.get("content-disposition")).toBe('attachment; filename="audit.csv"');
  });

  it("returns 502 when the backend response exceeds the BFF size bound", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    // ~1 MiB of payload with a tiny env-tuned cap exercises the abort path
    // without allocating 50 MiB.
    process.env.BFF_MAX_RESPONSE_BYTES = "1024";
    try {
      const big = "x".repeat(1024 * 1024);
      const fetchSpy = vi.fn().mockResolvedValue(
        new Response(big, { status: 200, headers: { "content-type": "application/json" } }),
      );
      global.fetch = fetchSpy as unknown as typeof fetch;

      const req = makeRequest("http://localhost/api/v1/users");
      const res = await GET(req, { params: Promise.resolve({ path: ["users"] }) });

      expect(res.status).toBe(502);
      const body = await res.json();
      expect(body.error).toContain("too large");
    } finally {
      delete process.env.BFF_MAX_RESPONSE_BYTES;
    }
  });

  // --- #180: a malformed Authorization header must not produce a 500 ---
  it("answers 401 (not 500) when getToken throws on a malformed Authorization header", async () => {
    // next-auth's getToken() decoded the bearer value with decodeURIComponent
    // outside its try block, so "Bearer %" raised a URIError.
    getTokenMock.mockRejectedValue(new URIError("URI malformed"));
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/users", {
      headers: { authorization: "Bearer %" },
    });
    const res = await GET(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(401);
    const body = await res.json();
    expect(body.error).toBe("unauthorized");
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  // --- #186: CSRF defence on state-changing requests ---
  it("rejects a state-changing request whose Origin is not the app's own origin with 403", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/users", {
      method: "POST",
      body: JSON.stringify({ name: "x" }),
      headers: { "content-type": "application/json", origin: "https://evil.example" },
    });
    const res = await POST(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(403);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("rejects a state-changing request with Sec-Fetch-Site: cross-site with 403", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/users", {
      method: "POST",
      body: JSON.stringify({ name: "x" }),
      headers: { "content-type": "application/json", "sec-fetch-site": "cross-site" },
    });
    const res = await POST(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(403);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("allows a same-origin state-changing request", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ success: true, data: {} }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    ) as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/users", {
      method: "POST",
      body: JSON.stringify({ name: "x" }),
      headers: { "content-type": "application/json", origin: "http://localhost" },
    });
    const res = await POST(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(200);
  });

  it("allows a state-changing request from the public origin behind a reverse proxy", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ success: true, data: {} }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    ) as unknown as typeof fetch;

    const req = makeRequest("http://frontend:3000/api/v1/users", {
      method: "POST",
      body: JSON.stringify({ name: "x" }),
      headers: {
        "content-type": "application/json",
        origin: "https://app.example.com",
        "x-forwarded-host": "app.example.com",
        "x-forwarded-proto": "https",
      },
    });
    const res = await POST(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(200);
  });

  it("does not apply the CSRF check to safe methods", async () => {    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ success: true, data: [] }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    ) as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/users", {
      headers: { origin: "https://evil.example" },
    });
    const res = await GET(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(200);
  });

  // --- #185: request body size limit ---
  it("rejects an oversized request body with 413 before reading it", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    const fetchSpy = vi.fn();
    global.fetch = fetchSpy as unknown as typeof fetch;
    process.env.BFF_MAX_REQUEST_BYTES = "16";
    try {
      const req = makeRequest("http://localhost/api/v1/users", {
        method: "POST",
        body: "x".repeat(64),
        headers: { "content-type": "application/json" },
      });
      // The fetch Request constructor drops Content-Length; a real inbound
      // request carries it on the wire.
      req.headers.set("content-length", "64");
      const res = await POST(req, { params: Promise.resolve({ path: ["users"] }) });

      expect(res.status).toBe(413);
      const body = await res.json();
      expect(body.error).toContain("too large");
      expect(fetchSpy).not.toHaveBeenCalled();
    } finally {
      delete process.env.BFF_MAX_REQUEST_BYTES;
    }
  });

  it("rejects a chunked oversized request body with 413 while streaming it", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    process.env.BFF_MAX_REQUEST_BYTES = "16";
    try {
      // A stream body is sent without a Content-Length, so the cap has to be
      // enforced while the body is forwarded.
      const stream = new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(new TextEncoder().encode("y".repeat(128)));
          controller.close();
        },
      });
      global.fetch = vi.fn(async (_url: unknown, init: { body: ReadableStream<Uint8Array> }) => {
        const reader = init.body.getReader();
        for (;;) {
          const { done } = await reader.read();
          if (done) break;
        }
        return new Response("{}", { status: 200 });
      }) as unknown as typeof fetch;

      const req = makeStreamingRequest("http://localhost/api/v1/users", stream);
      const res = await POST(req, { params: Promise.resolve({ path: ["users"] }) });

      expect(res.status).toBe(413);
    } finally {
      delete process.env.BFF_MAX_REQUEST_BYTES;
    }
  });

  // --- #184: upstream timeout ---
  it("answers 504 when the backend never responds", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    process.env.BFF_TIMEOUT_MS = "50";
    try {
      global.fetch = vi.fn((_url: unknown, init: { signal: AbortSignal }) => {
        return new Promise((_resolve, reject) => {
          init.signal.addEventListener("abort", () => reject(init.signal.reason));
        });
      }) as unknown as typeof fetch;

      const req = makeRequest("http://localhost/api/v1/users");
      const res = await GET(req, { params: Promise.resolve({ path: ["users"] }) });

      expect(res.status).toBe(504);
      const body = await res.json();
      expect(body.error).toContain("timeout");
    } finally {
      delete process.env.BFF_TIMEOUT_MS;
    }
  });

  // --- #196: an expired access token is refreshed before it is forwarded ---
  it("refreshes an expired access token before forwarding it to the backend", async () => {
    getTokenMock.mockResolvedValue({
      accessToken: "stale-access-token",
      refreshToken: "refresh-token",
      expiresAt: Math.floor(Date.now() / 1000) - 60,
    });
    const backendCalls: string[] = [];
    global.fetch = vi.fn(async (url: string, init: { headers: Headers }) => {
      const target = String(url);
      if (target.includes("/protocol/openid-connect/token")) {
        return new Response(
          JSON.stringify({
            access_token: "fresh-access-token",
            id_token: "fresh-id-token",
            expires_in: 300,
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }
      backendCalls.push(target);
      expect(init.headers.get("Authorization")).toBe("Bearer fresh-access-token");
      return new Response(JSON.stringify({ success: true, data: [] }), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }) as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/users");
    const res = await GET(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(200);
    expect(backendCalls).toEqual(["http://localhost:8080/api/v1/users"]);
  });

  // --- #193: raw upstream error text must not reach the browser ---
  it("replaces a raw upstream error body with a generic message", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    global.fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          success: false,
          error: "pq: connection refused to postgres://fc:secret@db:5432/freecloud",
        }),
        { status: 500, headers: { "content-type": "application/json" } },
      ),
    ) as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/users");
    const res = await GET(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(500);
    const text = await res.text();
    expect(text).not.toContain("postgres://fc:secret@db:5432");
    expect(text).toContain("Request failed with status 500");
  });

  it("replaces a non-JSON upstream error body with a generic message", async () => {
    getTokenMock.mockResolvedValue({ accessToken: "tok" });
    global.fetch = vi.fn().mockResolvedValue(
      new Response("<html>panic: runtime error at /srv/backend/main.go:42</html>", {
        status: 502,
        headers: { "content-type": "text/html" },
      }),
    ) as unknown as typeof fetch;

    const req = makeRequest("http://localhost/api/v1/users");
    const res = await GET(req, { params: Promise.resolve({ path: ["users"] }) });

    expect(res.status).toBe(502);
    const text = await res.text();
    expect(text).not.toContain("/srv/backend/main.go");
    expect(text).toContain("Request failed with status 502");
  });
});

describe("production configuration", () => {
  const origNodeEnv = (process.env as Record<string, string | undefined>).NODE_ENV;

  afterEach(() => {
    (process.env as Record<string, string | undefined>).NODE_ENV = origNodeEnv;
    delete process.env.INTERNAL_API_URL;
    delete process.env.AUTH_KEYCLOAK_ID;
    delete process.env.AUTH_KEYCLOAK_ISSUER;
  });

  // --- #181: the app must fail closed when INTERNAL_API_URL is missing ---
  it("refuses to load in production when INTERNAL_API_URL is unset", async () => {
    vi.resetModules();
    (process.env as Record<string, string | undefined>).NODE_ENV = "production";
    process.env.AUTH_KEYCLOAK_ID = "freecloud-dashboard";
    process.env.AUTH_KEYCLOAK_ISSUER = "https://auth.example.com/realms/freecloud";
    delete process.env.INTERNAL_API_URL;

    await expect(import("./route")).rejects.toThrow(/INTERNAL_API_URL/);
  });

  it("loads in production when INTERNAL_API_URL is set", async () => {
    vi.resetModules();
    (process.env as Record<string, string | undefined>).NODE_ENV = "production";
    process.env.AUTH_KEYCLOAK_ID = "freecloud-dashboard";
    process.env.AUTH_KEYCLOAK_ISSUER = "https://auth.example.com/realms/freecloud";
    process.env.INTERNAL_API_URL = "http://backend:8080";

    const mod = await import("./route");
    expect(typeof mod.GET).toBe("function");
  });
});

describe("readBoundedBody", () => {  it("returns the full body when under the limit", async () => {
    const res = new Response("hello world");
    const out = await readBoundedBody(res, 1024);
    expect(out.ok).toBe(true);
    if (out.ok) {
      expect(new TextDecoder().decode(out.bytes)).toBe("hello world");
    }
  });

  it("aborts (ok:false) when the body exceeds the limit", async () => {
    const res = new Response("this is way too long for a 4-byte limit");
    const out = await readBoundedBody(res, 4);
    expect(out.ok).toBe(false);
  });

  it("handles an empty body", async () => {
    const res = new Response(null, { status: 204 });
    const out = await readBoundedBody(res, 1024);
    expect(out.ok).toBe(true);
    if (out.ok) {
      expect(out.bytes.byteLength).toBe(0);
    }
  });
});
