import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// We test the module's exported functions by re-importing with controlled
// env vars. Because Node caches modules, we use dynamic import + vi.resetModules.
async function loadEnv() {
  vi.resetModules();
  return (await import("./env")) as typeof import("./env");
}

describe("env helpers", () => {
  const origEnv = { ...process.env };

  beforeEach(() => {
    process.env = { ...origEnv };
  });

  afterEach(() => {
    process.env = { ...origEnv };
  });

  describe("requiredEnv", () => {
    it("returns the value when set", async () => {
      process.env.MY_VAR = "hello";
      const { requiredEnv } = await loadEnv();
      expect(requiredEnv("MY_VAR")).toBe("hello");
    });

    it("throws when unset", async () => {
      delete process.env.MISSING_VAR;
      const { requiredEnv } = await loadEnv();
      expect(() => requiredEnv("MISSING_VAR")).toThrow("Missing required");
    });

    it("throws when empty string", async () => {
      process.env.EMPTY_VAR = "";
      const { requiredEnv } = await loadEnv();
      expect(() => requiredEnv("EMPTY_VAR")).toThrow("Missing required");
    });
  });

  describe("requiredProdEnv", () => {
    it("returns the value when set", async () => {
      process.env.KC_ID = "my-client";
      const { requiredProdEnv } = await loadEnv();
      expect(requiredProdEnv("KC_ID", "fallback")).toBe("my-client");
    });

    it("uses fallback in development", async () => {
      vi.stubEnv("NODE_ENV", "development");
      delete process.env.KC_ID;
      const { requiredProdEnv } = await loadEnv();
      expect(requiredProdEnv("KC_ID", "fallback")).toBe("fallback");
    });

    it("throws in production when unset", async () => {
      vi.stubEnv("NODE_ENV", "production");
      delete process.env.KC_SECRET;
      const { requiredProdEnv } = await loadEnv();
      expect(() => requiredProdEnv("KC_SECRET", "")).toThrow(
        "Missing required environment variable in production",
      );
    });
  });

  describe("isProduction", () => {
    it("returns false in development", async () => {
      vi.stubEnv("NODE_ENV", "development");
      const { isProduction } = await loadEnv();
      expect(isProduction()).toBe(false);
    });

    it("returns true in production", async () => {
      vi.stubEnv("NODE_ENV", "production");
      const { isProduction } = await loadEnv();
      expect(isProduction()).toBe(true);
    });
  });
});
