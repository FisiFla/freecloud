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

  describe("rejectInsecureInProd", () => {
    it("throws in production for a known placeholder secret", async () => {
      vi.stubEnv("NODE_ENV", "production");
      const { rejectInsecureInProd } = await loadEnv();
      expect(() =>
        rejectInsecureInProd("AUTH_SECRET", "change-me-to-a-random-string"),
      ).toThrow("placeholder");
    });

    it("does not throw in development for a placeholder", async () => {
      vi.stubEnv("NODE_ENV", "development");
      const { rejectInsecureInProd } = await loadEnv();
      expect(() =>
        rejectInsecureInProd("AUTH_SECRET", "change-me-to-a-random-string"),
      ).not.toThrow();
    });

    it("does not throw in production for a real secret", async () => {
      vi.stubEnv("NODE_ENV", "production");
      const { rejectInsecureInProd } = await loadEnv();
      // A compliant secret: unique and at least MIN_SECRET_LENGTH chars.
      expect(() =>
        rejectInsecureInProd("AUTH_SECRET", "a-genuinely-random-secret-value-0123456789"),
      ).not.toThrow();
    });

    it("throws in production for a short secret even when it is not a placeholder", async () => {
      vi.stubEnv("NODE_ENV", "production");
      const { rejectInsecureInProd } = await loadEnv();
      expect(() => rejectInsecureInProd("AUTH_SECRET", "short-but-unique")).toThrow(
        /at least 32 characters/,
      );
    });

    it("accepts a 32-character secret in production (boundary)", async () => {
      vi.stubEnv("NODE_ENV", "production");
      const { rejectInsecureInProd } = await loadEnv();
      expect(() => rejectInsecureInProd("AUTH_SECRET", "x".repeat(32))).not.toThrow();
    });

    it("accepts a 64-character generated secret in production", async () => {
      vi.stubEnv("NODE_ENV", "production");
      const { rejectInsecureInProd } = await loadEnv();
      // generate.sh writes 32 random bytes as 64 hex chars.
      expect(() => rejectInsecureInProd("AUTH_SECRET", "ab".repeat(32))).not.toThrow();
    });

    it("does not throw in development for a short secret", async () => {
      vi.stubEnv("NODE_ENV", "development");
      const { rejectInsecureInProd } = await loadEnv();
      expect(() => rejectInsecureInProd("AUTH_SECRET", "short-but-unique")).not.toThrow();
    });

    it("skips validation during the Next.js build phase", async () => {
      vi.stubEnv("NODE_ENV", "production");
      vi.stubEnv("NEXT_PHASE", "phase-production-build");
      const { rejectInsecureInProd } = await loadEnv();
      expect(() =>
        rejectInsecureInProd("AUTH_SECRET", "change-me-to-a-random-string"),
      ).not.toThrow();
      expect(() => rejectInsecureInProd("AUTH_SECRET", "short")).not.toThrow();
    });
  });

  describe("validateProdIssuer", () => {
    it("accepts an https issuer on a public host in production", async () => {
      vi.stubEnv("NODE_ENV", "production");
      const { validateProdIssuer } = await loadEnv();
      expect(() =>
        validateProdIssuer("https://auth.example.com/realms/freecloud"),
      ).not.toThrow();
    });

    it("throws in production for an http issuer", async () => {
      vi.stubEnv("NODE_ENV", "production");
      const { validateProdIssuer } = await loadEnv();
      expect(() =>
        validateProdIssuer("http://keycloak:8080/realms/freecloud"),
      ).toThrow(/https/);
    });

    it("throws in production for a malformed issuer", async () => {
      vi.stubEnv("NODE_ENV", "production");
      const { validateProdIssuer } = await loadEnv();
      expect(() => validateProdIssuer("not-a-url")).toThrow(/AUTH_KEYCLOAK_ISSUER/);
    });

    it.each(["localhost", "auth.localhost", "127.0.0.1", "127.0.0.2", "[::1]"])(
      "warns but does not throw in production for an https loopback issuer (%s)",
      async (host) => {
        vi.stubEnv("NODE_ENV", "production");
        const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
        try {
          const { validateProdIssuer } = await loadEnv();
          expect(() => validateProdIssuer(`https://${host}/realms/freecloud`)).not.toThrow();
          expect(errorSpy).toHaveBeenCalled();
        } finally {
          errorSpy.mockRestore();
        }
      },
    );

    it("logs but does not throw in production for an http loopback issuer (quickstart stack)", async () => {
      vi.stubEnv("NODE_ENV", "production");
      const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
      try {
        const { validateProdIssuer } = await loadEnv();
        expect(() => validateProdIssuer("http://localhost:8081/realms/freecloud")).not.toThrow();
        expect(errorSpy).toHaveBeenCalled();
      } finally {
        errorSpy.mockRestore();
      }
    });

    it("does not throw in development for an http localhost issuer", async () => {
      vi.stubEnv("NODE_ENV", "development");
      const { validateProdIssuer } = await loadEnv();
      expect(() =>
        validateProdIssuer("http://localhost:8081/realms/freecloud"),
      ).not.toThrow();
    });

    it("skips validation during the Next.js build phase", async () => {
      vi.stubEnv("NODE_ENV", "production");
      vi.stubEnv("NEXT_PHASE", "phase-production-build");
      const { validateProdIssuer } = await loadEnv();
      expect(() => validateProdIssuer("http://keycloak:8080/realms/freecloud")).not.toThrow();
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
