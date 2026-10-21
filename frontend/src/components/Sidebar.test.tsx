import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

vi.mock("next/navigation", () => ({ usePathname: () => "/employees" }));
vi.mock("next-auth/react", () => ({
  useSession: () => ({ data: null, status: "unauthenticated" }),
  signOut: vi.fn(),
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
