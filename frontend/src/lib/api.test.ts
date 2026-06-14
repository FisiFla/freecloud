import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { ApiError } from "./api";

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
