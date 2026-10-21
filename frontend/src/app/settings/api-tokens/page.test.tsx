import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";

const { listAPITokensMock, createAPITokenMock } = vi.hoisted(() => ({
  listAPITokensMock: vi.fn(),
  createAPITokenMock: vi.fn(),
}));

vi.mock("../../providers", () => ({ useApiReady: () => true }));

vi.mock("@/lib/api", () => ({
  listAPITokens: listAPITokensMock,
  createAPIToken: createAPITokenMock,
  revokeAPIToken: vi.fn(),
}));

import APITokensPage from "./page";

describe("API tokens page timers", () => {
  beforeEach(() => {
    listAPITokensMock.mockReset();
    createAPITokenMock.mockReset();
    listAPITokensMock.mockResolvedValue([]);
    createAPITokenMock.mockResolvedValue({
      id: "t1",
      name: "ci",
      token: "secret-token-value",
      role: "read-only",
      serviceIdentity: "github-actions",
      createdAt: new Date().toISOString(),
    });
    Object.assign(navigator, {
      clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  // --- #192: the pending timer must be cleared when the page unmounts ---
  it("clears the pending copy timer on unmount", async () => {
    const clearSpy = vi.spyOn(globalThis, "clearTimeout");
    const { unmount } = render(<APITokensPage />);

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "ci" } });
    fireEvent.change(screen.getByLabelText("Service Identity"), {
      target: { value: "github-actions" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create token/i }));

    const copyButton = await screen.findByLabelText("Copy token");
    await act(async () => {
      fireEvent.click(copyButton);
    });

    clearSpy.mockClear();
    unmount();
    expect(clearSpy).toHaveBeenCalled();
  });

  it("renders the create form with associated labels", async () => {
    render(<APITokensPage />);
    await waitFor(() => expect(listAPITokensMock).toHaveBeenCalled());
    expect(screen.getByLabelText("Name")).toBeInTheDocument();
    expect(screen.getByLabelText("Role")).toBeInTheDocument();
    expect(screen.getByLabelText("Service Identity")).toBeInTheDocument();
  });
});
