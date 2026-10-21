import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { NextRequest } from "next/server";
import { buildCsp } from "@/lib/csp";

// The middleware wraps its handler with Auth.js's `auth()`. Stub it out so
// the handler can be called directly with a plain NextRequest.
vi.mock("@/auth", () => ({
  auth: (handler: unknown) => handler,
}));

import proxy, { config as proxyConfig } from "./proxy";

type MiddlewareHandler = (
  req: NextRequest,
  event: unknown,
) => Promise<Response> | Response;

const handler = proxy as unknown as MiddlewareHandler;

function makeRequest(url: string, opts: { auth?: unknown } = {}): NextRequest {
  const req = new NextRequest(new Request(url));
  (req as unknown as { auth?: unknown }).auth = opts.auth ?? null;
  return req;
}

const EVENT = {} as never;

describe("buildCsp (nonce-based CSP)", () => {
  const origNodeEnv = (process.env as Record<string, string | undefined>).NODE_ENV;
  afterEach(() => {
    // Restore the original env after each test (vitest sets NODE_ENV).
    (process.env as Record<string, string | undefined>).NODE_ENV = origNodeEnv;
  });

  function setNodeEnv(v: "production" | "development" | "test" | undefined) {
    if (v === undefined) delete (process.env as Record<string, string | undefined>).NODE_ENV;
    else (process.env as Record<string, string>).NODE_ENV = v;
  }

  it("drops unsafe-inline and unsafe-eval from script-src in production, uses the nonce", () => {
    setNodeEnv("production");
    const csp = buildCsp("abc123nonce");
    expect(csp).toContain("script-src 'self' 'nonce-abc123nonce'");
    // script-src must not allow inline/eval — the nonce is the only escape hatch.
    expect(csp).not.toMatch(/script-src[^;]*'unsafe-inline'/);
    expect(csp).not.toMatch(/script-src[^;]*'unsafe-eval'/);
  });

  it("keeps the rest of the policy directives", () => {
    setNodeEnv("production");
    const csp = buildCsp("n1");
    expect(csp).toContain("default-src 'self'");
    expect(csp).toContain("frame-ancestors 'none'");
    expect(csp).toContain("base-uri 'self'");
    expect(csp).toContain("form-action 'self'");
  });

  it("allows unsafe-eval outside production for Next.js HMR", () => {
    setNodeEnv("development");
    const csp = buildCsp("n1");
    expect(csp).toContain("'unsafe-eval'");
    expect(csp).not.toContain("'nonce-");
  });

  // --- #195: the production policy must not allow an arbitrary origin ---
  it("restricts connect-src to 'self' in production and adds object-src / upgrade-insecure-requests", () => {
    setNodeEnv("production");
    const csp = buildCsp("n1");
    const connectSrc = csp.split(";").find((d) => d.trim().startsWith("connect-src"));
    expect(connectSrc).toBeDefined();
    expect(connectSrc).not.toContain("https:");
    expect(connectSrc).not.toContain("http://localhost");
    expect(connectSrc?.trim()).toBe("connect-src 'self'");
    expect(csp).toContain("object-src 'none'");
    expect(csp).toContain("upgrade-insecure-requests");
  });

  it("keeps the localhost development entries out of the production policy", () => {
    setNodeEnv("production");
    const csp = buildCsp("n1");
    expect(csp).not.toContain("localhost:8080");
    expect(csp).not.toContain("localhost:8081");
  });
});

describe("middleware (src/proxy.ts)", () => {
  const origFetch = global.fetch;

  beforeEach(() => {
    // The setup-status probe must not reach the network in tests.
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ success: true, data: { provisioned: true } }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    ) as unknown as typeof fetch;
  });

  afterEach(() => {
    global.fetch = origFetch;
  });

  it("excludes every /api route from the matcher", () => {
    expect(proxyConfig.matcher).toEqual([
      "/((?!api|_next/static|_next/image|favicon.ico).*)",
    ]);
  });

  it("redirects an unauthenticated page request to /signin", async () => {
    const res = await handler(makeRequest("http://localhost/employees"), EVENT);
    expect(res.status).toBe(307);
    const location = res.headers.get("location") ?? "";
    expect(location).toContain("/signin");
    expect(location).toContain("callbackUrl");
  });

  it("lets an authenticated page request through", async () => {
    const res = await handler(
      makeRequest("http://localhost/employees", { auth: { user: { sub: "u1" } } }),
      EVENT,
    );
    expect(res.status).toBe(200);
    expect(res.headers.get("location")).toBeNull();
  });

  // --- #182: the nonce must reach the app on the REQUEST headers ---
  it("forwards the CSP nonce on the request headers and keeps the CSP on the response", async () => {
    vi.stubEnv("NODE_ENV", "production");
    try {
      const res = await handler(
        makeRequest("http://localhost/signin", { auth: null }),
        EVENT,
      );
      const overridden = res.headers.get("x-middleware-override-headers") ?? "";
      expect(overridden.split(",")).toContain("x-nonce");
      const nonce = res.headers.get("x-middleware-request-x-nonce");
      expect(nonce).toBeTruthy();
      const csp = res.headers.get("content-security-policy") ?? "";
      expect(csp).toContain(`script-src 'self' 'nonce-${nonce}'`);
    } finally {
      vi.unstubAllEnvs();
    }
  });

  it("does not echo the nonce back as a response header", async () => {
    const res = await handler(
      makeRequest("http://localhost/signin", { auth: null }),
      EVENT,
    );
    expect(res.headers.get("x-nonce")).toBeNull();
  });

  it("fails open when the setup-status probe cannot reach the backend", async () => {
    global.fetch = vi.fn().mockRejectedValue(new Error("ECONNREFUSED")) as unknown as typeof fetch;
    const res = await handler(
      makeRequest("http://localhost/employees", { auth: { user: { sub: "u1" } } }),
      EVENT,
    );
    // Not redirected to /setup — a network error must not lock every user out.
    expect(res.headers.get("location")).toBeNull();
    expect(res.status).toBe(200);
  });

  it("redirects to /setup when the backend reports the deployment is not provisioned", async () => {
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ success: true, data: { provisioned: false } }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    ) as unknown as typeof fetch;
    const res = await handler(
      makeRequest("http://localhost/employees", { auth: { user: { sub: "u1" } } }),
      EVENT,
    );
    expect(res.status).toBe(307);
    expect(res.headers.get("location")).toContain("/setup");
  });
});
