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

import { GET, POST } from "./route";

function makeRequest(url: string, init?: RequestInit): NextRequest {
  return new NextRequest(new Request(url, init));
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
});
