import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, act, waitFor } from "@testing-library/react";

const { useParamsMock, getUserMock, getMFAStatusMock, listPoliciesMock } = vi.hoisted(() => ({
  useParamsMock: vi.fn(),
  getUserMock: vi.fn(),
  getMFAStatusMock: vi.fn(),
  listPoliciesMock: vi.fn(),
}));

vi.mock("next/navigation", () => ({ useParams: () => useParamsMock() }));
vi.mock("../../providers", () => ({ useApiReady: () => true }));

vi.mock("@/lib/api", () => ({
  offboardUser: vi.fn(),
  getUser: getUserMock,
  patchUser: vi.fn(),
  resetPassword: vi.fn(),
  getMFAStatus: getMFAStatusMock,
  requireMFA: vi.fn(),
  lockDevice: vi.fn(),
  listPolicies: listPoliciesMock,
  restartDevice: vi.fn(),
  lockDeviceWithMessage: vi.fn(),
  clearPasscode: vi.fn(),
  getDeviceCommandHistory: vi.fn(),
}));

import EmployeeDetailPage from "./page";

describe("employee detail loader", () => {
  beforeEach(() => {
    useParamsMock.mockReset();
    getUserMock.mockReset();
    getMFAStatusMock.mockReset();
    listPoliciesMock.mockReset();
    listPoliciesMock.mockResolvedValue({ policies: [] });
    getUserMock.mockImplementation((id: string) =>
      Promise.resolve({
        id,
        firstName: "First",
        lastName: "Last",
        email: "first@example.com",
        department: "",
        role: "",
        disabled: false,
        isFederated: false,
        devices: [],
      }),
    );
  });

  // --- #194: a late response for the previous employee must be discarded ---
  it("does not apply a stale MFA status after the employee id changes", async () => {
    let resolveStale: ((value: unknown) => void) | undefined;
    getMFAStatusMock.mockImplementation((id: string) => {
      if (id === "user-1") {
        return new Promise((resolve) => {
          resolveStale = resolve;
        });
      }
      return Promise.resolve({
        userId: id,
        otpEnabled: false,
        otpPending: false,
        webAuthnEnabled: false,
      });
    });

    useParamsMock.mockReturnValue({ id: "user-1" });
    const { rerender } = render(<EmployeeDetailPage />);
    await waitFor(() => expect(getUserMock).toHaveBeenCalledWith("user-1"));

    // Navigate to another employee before the first MFA request resolves.
    useParamsMock.mockReturnValue({ id: "user-2" });
    rerender(<EmployeeDetailPage />);
    await waitFor(() => expect(getMFAStatusMock).toHaveBeenCalledWith("user-2"));

    // The stale response for user-1 arrives late.
    await act(async () => {
      resolveStale?.({
        userId: "user-1",
        otpEnabled: true,
        otpPending: false,
        webAuthnEnabled: true,
      });
    });

    // The panel must show user-2's status, not user-1's.
    await waitFor(() =>
      expect(screen.getAllByText(/:\s*Not enrolled$/).length).toBeGreaterThan(0),
    );
    // "Enrolled" (as opposed to "Not enrolled") would mean the stale
    // response for user-1 was applied.
    expect(screen.queryAllByText(/:\s*Enrolled$/)).toHaveLength(0);
  });
});
