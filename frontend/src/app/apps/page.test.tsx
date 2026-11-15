import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import AppsPage from "./page";
import { listApps, listUsers, getAppPolicy } from "@/lib/api";
import type { MeResponse } from "@/lib/api";

const { useOrgMock } = vi.hoisted(() => ({ useOrgMock: vi.fn() }));
vi.mock("@/app/providers", () => ({
  useApiReady: () => true,
  useOrg: (...args: unknown[]) => useOrgMock(...args),
}));

vi.mock("@/lib/api", () => ({
  listApps: vi.fn(),
  createApp: vi.fn(),
  listUsers: vi.fn(),
  assignAppToUser: vi.fn(),
  getAppPolicy: vi.fn(),
  upsertAppPolicy: vi.fn(),
  downloadSAMLMetadata: vi.fn(),
}));

const listAppsMock = vi.mocked(listApps);
const listUsersMock = vi.mocked(listUsers);
const getAppPolicyMock = vi.mocked(getAppPolicy);

function meAs(admin: boolean): MeResponse {
  return {
    sub: "u1",
    email: "user@example.com",
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

describe("apps page access-policy dialog (app/apps)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listAppsMock.mockResolvedValue([
      { id: "app-1", keycloakClientId: "files", name: "Files", protocol: "OIDC", enabled: true },
    ]);
    listUsersMock.mockResolvedValue([]);
    getAppPolicyMock.mockResolvedValue({
      appId: "app-1",
      requireEnrolled: true,
      requireDiskEncrypted: false,
      requireNoCriticalVulns: false,
    });
  });

  async function openPolicyDialog() {
    render(<AppsPage />);
    const openButton = await screen.findByRole("button", { name: /access policy/i });
    fireEvent.click(openButton);
    return screen.findByRole("button", { name: /save policy/i });
  }

  it("lets super-admins edit and save the policy", async () => {
    mockOrg(true);
    const save = await openPolicyDialog();
    expect(save).toBeEnabled();
    expect(screen.getByLabelText(/require device enrolled in mdm/i)).toBeEnabled();
  });

  it("shows the policy read-only to non-super-admin roles (PUT is super-admin only)", async () => {
    mockOrg(false);
    const save = await openPolicyDialog();
    expect(save).toBeDisabled();
    const checkbox = screen.getByLabelText(/require device enrolled in mdm/i);
    expect(checkbox).toBeDisabled();
    expect(checkbox).toBeChecked();
    expect(screen.getByText(/super-admin/i)).toBeInTheDocument();
  });
});
