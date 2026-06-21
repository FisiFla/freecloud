import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import DarkModeToggle from "./DarkModeToggle";

// Mock localStorage
const localStorageMock = (() => {
  let store: Record<string, string> = {};
  return {
    getItem: (key: string) => store[key] ?? null,
    setItem: (key: string, value: string) => { store[key] = value; },
    clear: () => { store = {}; },
  };
})();

Object.defineProperty(window, "localStorage", { value: localStorageMock });

describe("DarkModeToggle", () => {
  beforeEach(() => {
    localStorageMock.clear();
    document.documentElement.classList.remove("dark");
  });

  it("renders with aria-label='Toggle dark mode' after mount", async () => {
    await act(async () => {
      render(<DarkModeToggle />);
    });
    expect(screen.getByLabelText("Toggle dark mode")).toBeInTheDocument();
  });

  it("has aria-pressed=false when not in dark mode", async () => {
    await act(async () => {
      render(<DarkModeToggle />);
    });
    const btn = screen.getByLabelText("Toggle dark mode");
    expect(btn).toHaveAttribute("aria-pressed", "false");
  });

  it("toggles aria-pressed to true when clicked", async () => {
    await act(async () => {
      render(<DarkModeToggle />);
    });
    const btn = screen.getByLabelText("Toggle dark mode");
    await act(async () => {
      fireEvent.click(btn);
    });
    expect(btn).toHaveAttribute("aria-pressed", "true");
  });

  it("adds dark class to documentElement when toggled on", async () => {
    await act(async () => {
      render(<DarkModeToggle />);
    });
    await act(async () => {
      fireEvent.click(screen.getByLabelText("Toggle dark mode"));
    });
    expect(document.documentElement.classList.contains("dark")).toBe(true);
  });
});
