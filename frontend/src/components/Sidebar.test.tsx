import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

const { useSessionMock, federatedSignOutMock } = vi.hoisted(() => ({
  useSessionMock: vi.fn(),
  federatedSignOutMock: vi.fn(),
}));
vi.mock("next/navigation", () => ({ usePathname: () => "/employees" }));
vi.mock("next-auth/react", () => ({
  useSession: (...args: unknown[]) => useSessionMock(...args),
  signOut: vi.fn(),
}));
vi.mock("@/lib/federated-logout", () => ({
  federatedSignOut: (...args: unknown[]) => federatedSignOutMock(...args),
}));
vi.mock("@/app/providers", () => ({
  useOrg: () => ({
    me: null,
    loading: false,
    error: null,
    activeOrgId: "",
    setActiveOrg: vi.fn(),
    refresh: vi.fn(),
  }),
}));

import Sidebar from "./Sidebar";

// jsdom does not expose localStorage on the default opaque origin.
const localStorageMock = (() => {
  let store: Record<string, string> = {};
  return {
    getItem: (key: string) => store[key] ?? null,
    setItem: (key: string, value: string) => {
      store[key] = value;
    },
    clear: () => {
      store = {};
    },
  };
})();
Object.defineProperty(window, "localStorage", { value: localStorageMock });

beforeEach(() => {
  vi.clearAllMocks();
  useSessionMock.mockReturnValue({ data: null, status: "unauthenticated" });
});

describe("Sidebar sign out", () => {
  it("is hidden when there is no session", () => {
    render(<Sidebar />);
    expect(screen.queryByLabelText("Sign out")).toBeNull();
  });

  it("runs federated sign-out (ending the Keycloak SSO session too)", () => {
    useSessionMock.mockReturnValue({
      data: { user: { name: "Op", email: "op@example.com" } },
      status: "authenticated",
    });
    render(<Sidebar />);
    fireEvent.click(screen.getByLabelText("Sign out"));
    expect(federatedSignOutMock).toHaveBeenCalledTimes(1);
  });
});

describe("Sidebar mobile drawer", () => {
  it("is not rendered until it is opened", () => {
    render(<Sidebar />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  // --- #191: the drawer must be keyboard accessible ---
  it("moves focus into the drawer when it opens", () => {
    render(<Sidebar />);
    const trigger = screen.getByLabelText("Open sidebar");
    trigger.focus();
    fireEvent.click(trigger);

    const drawer = screen.getByRole("dialog");
    expect(drawer).toBeInTheDocument();
    expect(drawer.contains(document.activeElement)).toBe(true);
  });

  it("closes the drawer on Escape", () => {
    render(<Sidebar />);
    fireEvent.click(screen.getByLabelText("Open sidebar"));
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("closes the drawer when the backdrop is clicked", () => {
    render(<Sidebar />);
    fireEvent.click(screen.getByLabelText("Open sidebar"));
    const backdrop = document.querySelector('[aria-hidden="true"]') as HTMLElement;
    expect(backdrop).toBeTruthy();
    fireEvent.click(backdrop);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("restores focus to the hamburger button when the drawer closes", () => {
    render(<Sidebar />);
    const trigger = screen.getByLabelText("Open sidebar");
    trigger.focus();
    fireEvent.click(trigger);
    expect(document.activeElement).not.toBe(trigger);

    fireEvent.keyDown(document, { key: "Escape" });
    expect(document.activeElement).toBe(trigger);
  });
});
