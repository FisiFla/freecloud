import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import AppPolicyPage from "./page";
import { getAppPolicy } from "@/lib/api";
import type { MeResponse } from "@/lib/api";

vi.mock("next/navigation", () => ({
  useParams: () => ({ id: "app-1" }),
}));

const { useOrgMock } = vi.hoisted(() => ({ useOrgMock: vi.fn() }));
vi.mock("@/app/providers", () => ({
  useApiReady: () => true,
  useOrg: (...args: unknown[]) => useOrgMock(...args),
}));

vi.mock("@/lib/api", () => ({
  getAppPolicy: vi.fn(),
  upsertAppPolicy: vi.fn(),
  previewAppPolicy: vi.fn(),
}));

const getAppPolicyMock = vi.mocked(getAppPolicy);

const policyFixture = {
  appId: "app-1",
  requireEnrolled: true,
  requireDiskEncrypted: false,
  requireNoCriticalVulns: false,
  allowedTimeStart: "09:00",
  allowedTimeEnd: "18:00",
  networkAllowlist: [],
  geoCountryAllowlist: [],
};

function meAs(admin: boolean): MeResponse {
  return {
    sub: "u1",
    email: "admin@example.com",
    globalRole: admin ? "super-admin" : "user",
    activeOrgId: "o1",
    activeRole: admin ? "super-admin" : "helpdesk",
    orgs: [],
    isSystemAdmin: admin,
  };
}

function mockOrg(admin: boolean) {
  useOrgMock.mockReturnValue({
    me: meAs(admin),
    activeOrgId: "o1",
    setActiveOrg: vi.fn(),
    loading: false,
    error: null,
  });
}

describe("app policy page (apps/[id]/policy)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getAppPolicyMock.mockResolvedValue(policyFixture);
  });

  it("lets super-admins edit and save the policy", async () => {
    mockOrg(true);
    render(<AppPolicyPage />);
    const save = await screen.findByRole("button", { name: /save policy/i });
    expect(save).toBeEnabled();
    expect(screen.getByLabelText(/require device enrolled/i)).toBeEnabled();
  });

  it("shows the policy read-only to non-super-admin roles (PUT is super-admin only)", async () => {
    mockOrg(false);
    render(<AppPolicyPage />);
    const save = await screen.findByRole("button", { name: /save policy/i });
    expect(save).toBeDisabled();
    expect(screen.getByLabelText(/require device enrolled/i)).toBeDisabled();
    expect(screen.getByLabelText(/start/i)).toBeDisabled();
    // Values are still visible read-only.
    expect(screen.getByLabelText(/require device enrolled/i)).toBeChecked();
    // The page says why editing is unavailable.
    expect(screen.getByText(/super-admin/i)).toBeInTheDocument();
    // Preview (a dry-run read) stays available.
    expect(screen.getByRole("button", { name: /run preview/i })).toBeEnabled();
  });
});
