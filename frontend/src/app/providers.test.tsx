import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

const { useSessionMock, federatedSignOutMock } = vi.hoisted(() => ({
  useSessionMock: vi.fn(),
  federatedSignOutMock: vi.fn(),
}));
vi.mock("next-auth/react", () => ({
  SessionProvider: ({ children }: { children: React.ReactNode }) => children,
  useSession: (...args: unknown[]) => useSessionMock(...args),
}));
vi.mock("@/lib/federated-logout", () => ({
  federatedSignOut: (...args: unknown[]) => federatedSignOutMock(...args),
}));
vi.mock("@/lib/api", () => ({
  setActiveOrgId: vi.fn(),
  getMe: vi.fn(),
}));

import { Providers } from "./providers";
import { getMe } from "@/lib/api";

describe("Providers refresh-failure sign-out", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(getMe).mockResolvedValue({
      sub: "u1",
      email: "u@example.com",
      globalRole: "user",
      activeOrgId: "o1",
      activeRole: "member",
      orgs: [],
      isSystemAdmin: false,
    });
  });

  it("runs federated sign-out with the expired indication when the refresh fails", async () => {
    useSessionMock.mockReturnValue({
      data: { error: "RefreshAccessTokenError" },
      status: "authenticated",
    });
    render(
      <Providers>
        <div>child</div>
      </Providers>,
    );
    await waitFor(() => expect(federatedSignOutMock).toHaveBeenCalledWith({ expired: true }));
    expect(screen.getByText("child")).toBeInTheDocument();
  });

  it("does not sign out a healthy session", async () => {
    useSessionMock.mockReturnValue({ data: { user: { name: "Op" } }, status: "authenticated" });
    render(
      <Providers>
        <div>child</div>
      </Providers>,
    );
    expect(await screen.findByText("child")).toBeInTheDocument();
    // Let effects settle, then confirm no sign-out was triggered.
    await waitFor(() => expect(vi.mocked(getMe)).toHaveBeenCalled());
    expect(federatedSignOutMock).not.toHaveBeenCalled();
  });
});
