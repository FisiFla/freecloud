import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

async function loadSafeUrl(nodeEnv: string) {
  vi.resetModules();
  vi.stubEnv("NODE_ENV", nodeEnv);
  return await import("./safe-url");
}

describe("safeExternalUrl", () => {
  const origEnv = { ...process.env };

  beforeEach(() => {
    process.env = { ...origEnv };
  });

  afterEach(() => {
    process.env = { ...origEnv };
  });

  it.each([
    "https://portal.example.com/launch",
    "https://apps.example.com:8443/a/b?x=1#y",
    "HTTPS://UPPER.EXAMPLE.COM/x",
  ])("allows absolute https URLs (%s)", async (raw) => {
    const { safeExternalUrl } = await loadSafeUrl("test");
    expect(safeExternalUrl(raw)).toBe(raw);
  });

  it("trims surrounding whitespace before validating", async () => {
    const { safeExternalUrl } = await loadSafeUrl("test");
    expect(safeExternalUrl("  https://ok.example/app  \n")).toBe("https://ok.example/app");
  });

  it.each([
    "javascript:alert(1)",
    "JaVaScRiPt:alert(document.domain)",
    "data:text/html,<script>alert(1)</script>",
    "vbscript:msgbox(1)",
    "file:///etc/passwd",
    "ftp://files.example.com/x",
  ])("rejects dangerous schemes (%s)", async (raw) => {
    const { safeExternalUrl } = await loadSafeUrl("test");
    expect(safeExternalUrl(raw)).toBeNull();
  });

  it("rejects protocol-relative URLs", async () => {
    const { safeExternalUrl } = await loadSafeUrl("test");
    expect(safeExternalUrl("//evil.example.com/phish")).toBeNull();
  });

  it("rejects relative URLs", async () => {
    const { safeExternalUrl } = await loadSafeUrl("test");
    expect(safeExternalUrl("/api/v1/evil")).toBeNull();
  });

  it.each([
    "  javascript:alert(1)",
    "\u0000javascript:alert(1)",
    "\u0001https://ok.example/",
    "java\tscript:alert(1)",
    "java\nscript:alert(1)",
    "https://ok.exa\u0000mple.com/",
    "https://ok.example/\u007f",
  ])("rejects whitespace/control-character smuggling (%j)", async (raw) => {
    const { safeExternalUrl } = await loadSafeUrl("test");
    expect(safeExternalUrl(raw)).toBeNull();
  });

  it.each([null, undefined, "", "   "])("returns null for empty input (%j)", async (raw) => {
    const { safeExternalUrl } = await loadSafeUrl("test");
    expect(safeExternalUrl(raw)).toBeNull();
  });

  it("rejects plain http for non-loopback hosts in development", async () => {
    const { safeExternalUrl } = await loadSafeUrl("development");
    expect(safeExternalUrl("http://apps.example.com/")).toBeNull();
  });

  it.each([
    "http://localhost:3000/app",
    "http://localhost/app",
    "http://app.localhost/app",
    "http://127.0.0.1:8080/app",
    "http://[::1]/app",
  ])("allows http for loopback hosts in development (%s)", async (raw) => {
    const { safeExternalUrl } = await loadSafeUrl("development");
    expect(safeExternalUrl(raw)).toBe(raw);
  });

  it("rejects http even for loopback hosts in production", async () => {
    const { safeExternalUrl } = await loadSafeUrl("production");
    expect(safeExternalUrl("http://localhost:3000/app")).toBeNull();
    expect(safeExternalUrl("http://127.0.0.1/app")).toBeNull();
    expect(safeExternalUrl("https://portal.example.com/app")).toBe("https://portal.example.com/app");
  });
});
