import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { ApiError, listUsers, onboardEmployee, setActiveOrgId } from "./api";

describe("ApiError", () => {
  it("stores the message", () => {
    const err = new ApiError("something broke");
    expect(err.message).toBe("something broke");
    expect(err.name).toBe("ApiError");
  });

  it("stores field errors when provided", () => {
    const fieldErrors = [
      { field: "email", message: "email is required" },
      { field: "firstName", message: "too long" },
    ];
    const err = new ApiError("validation failed", fieldErrors);
    expect(err.fieldErrors).toEqual(fieldErrors);
    expect(err.fieldErrors).toHaveLength(2);
  });

  it("has undefined fieldErrors by default", () => {
    const err = new ApiError("plain error");
    expect(err.fieldErrors).toBeUndefined();
  });
});

describe("api request error parsing", () => {
  // These tests verify the ApiError shape that pages rely on for surfacing
  // field-level validation errors. The actual fetch is mocked.
  const origFetch = global.fetch;

  beforeEach(() => {
    vi.resetModules();
  });

  afterEach(() => {
    global.fetch = origFetch;
  });

  it("ApiError with fieldErrors is detectable via instanceof", () => {
    const err = new ApiError("msg", [{ field: "email", message: "bad" }]);
    expect(err instanceof ApiError).toBe(true);
    expect(err.fieldErrors?.length).toBe(1);
  });
});

describe("request()", () => {
  const origFetch = global.fetch;

  beforeEach(() => {
    vi.resetModules();
    setActiveOrgId(null);
  });

  afterEach(() => {
    global.fetch = origFetch;
    setActiveOrgId(null);
  });

  it("calls the same-origin BFF path, never the backend host", async () => {
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ success: true, data: [{ id: "u1" }] }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
    global.fetch = fetchSpy as unknown as typeof fetch;

    const users = await listUsers();

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const [url, init] = fetchSpy.mock.calls[0];
    expect(url).toBe("/api/v1/users");
    expect((init as RequestInit).method).toBe("GET");
    expect(users).toEqual([{ id: "u1" }]);
  });

  it("attaches the active organization id as X-Org-Id", async () => {
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ success: true, data: [] }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
    global.fetch = fetchSpy as unknown as typeof fetch;

    setActiveOrgId("org-123");
    await listUsers();

    const [, init] = fetchSpy.mock.calls[0];
    const headers = (init as RequestInit).headers as Record<string, string>;
    expect(headers["X-Org-Id"]).toBe("org-123");
  });

  // --- #193: the raw upstream body must never reach the browser ---
  it("throws a generic error for a non-JSON error response, without the raw body", async () => {
    global.fetch = vi.fn().mockResolvedValue(
      new Response("pq: connection refused to postgres://fc:secret@db:5432", {
        status: 500,
        headers: { "content-type": "text/plain" },
      }),
    ) as unknown as typeof fetch;

    await expect(listUsers()).rejects.toThrow("Request failed with status 500");
    await expect(listUsers()).rejects.not.toThrow(/postgres:\/\/fc:secret/);
  });

  it("maps the errors array to fieldErrors", async () => {
    global.fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          success: false,
          errors: [{ field: "email", message: "email is required" }],
        }),
        { status: 400, headers: { "content-type": "application/json" } },
      ),
    ) as unknown as typeof fetch;

    try {
      await onboardEmployee({
        firstName: "Jane",
        lastName: "Doe",
        email: "",
        department: "Eng",
        role: "Dev",
      });
      expect.fail("expected onboardEmployee to throw");
    } catch (err) {
      expect(err).toBeInstanceOf(ApiError);
      const apiErr = err as ApiError;
      expect(apiErr.fieldErrors).toEqual([
        { field: "email", message: "email is required" },
      ]);
      expect(apiErr.message).toBe("email is required");
    }
  });
});
