import { describe, it, expect, afterEach } from "vitest";
import { buildCsp } from "@/lib/csp";

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
});
