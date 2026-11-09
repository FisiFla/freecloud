import { describe, it, expect, vi } from "vitest";
import { createKeycloakFetch } from "./keycloak";

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
