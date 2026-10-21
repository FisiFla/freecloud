import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

const { useOrgMock, listOrgMembersMock, listOrgsMock } = vi.hoisted(() => ({
  useOrgMock: vi.fn(),
  listOrgMembersMock: vi.fn(),
  listOrgsMock: vi.fn(),
}));

vi.mock("../../providers", () => ({
  useApiReady: () => true,
  useOrg: () => useOrgMock(),
}));

vi.mock("@/lib/api", () => ({
  listOrgs: listOrgsMock,
  createOrg: vi.fn(),
  listOrgMembers: listOrgMembersMock,
  addOrgMember: vi.fn(),
  removeOrgMember: vi.fn(),
}));

import OrganizationsPage from "./page";

function me(overrides: Record<string, unknown> = {}) {
  return {
    me: {
      sub: "u1",
      email: "user@example.com",
      globalRole: "user",
      activeOrgId: "org-1",
      activeRole: "member",
      orgs: [
        { orgId: "org-1", orgName: "Acme", orgSlug: "acme", role: "member" },
      ],
      isSystemAdmin: false,
      ...overrides,
    },
    loading: false,
    error: null,
    activeOrgId: "org-1",
    setActiveOrg: vi.fn(),
    refresh: vi.fn(),
  };
}

describe("organizations page member management gating", () => {
  beforeEach(() => {
    useOrgMock.mockReset();
    listOrgMembersMock.mockReset();
    listOrgsMock.mockReset();
    listOrgsMock.mockResolvedValue([
      { id: "org-1", name: "Acme", slug: "acme", createdAt: "" },
    ]);
    listOrgMembersMock.mockResolvedValue([
      {
        userId: "u2",
        email: "other@example.com",
        firstName: "Other",
        lastName: "Person",
        role: "member",
      },
    ]);
  });

  // --- #188: the controls must be gated on the active role ---
  it("does not render the member controls for a plain member", async () => {
    useOrgMock.mockReturnValue(me());
    render(<OrganizationsPage />);

    await waitFor(() => expect(listOrgMembersMock).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: /add member/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /remove/i })).toBeNull();
  });

  it("renders the member controls for an org-admin", async () => {
    useOrgMock.mockReturnValue(
      me({
        activeRole: "org-admin",
        orgs: [
          { orgId: "org-1", orgName: "Acme", orgSlug: "acme", role: "org-admin" },
        ],
      }),
    );
    render(<OrganizationsPage />);

    await waitFor(() => expect(listOrgMembersMock).toHaveBeenCalled());
    expect(await screen.findByRole("button", { name: /add member/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /remove/i })).toBeInTheDocument();
  });

  it("renders the member controls for a system admin", async () => {
    useOrgMock.mockReturnValue(
      me({
        isSystemAdmin: true,
        globalRole: "super-admin",
        activeRole: "member",
      }),
    );
    render(<OrganizationsPage />);

    await waitFor(() => expect(listOrgMembersMock).toHaveBeenCalled());
    expect(await screen.findByRole("button", { name: /add member/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /remove/i })).toBeInTheDocument();
  });
});
