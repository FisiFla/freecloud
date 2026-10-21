import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import ConfirmDialog from "./ConfirmDialog";

const baseProps = {
  isOpen: true,
  onClose: vi.fn(),
  onConfirm: vi.fn(),
  title: "Delete item?",
  message: "This action cannot be undone.",
  confirmLabel: "Delete",
};

describe("ConfirmDialog", () => {
  it("does not render when isOpen is false", () => {
    render(<ConfirmDialog {...baseProps} isOpen={false} />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("renders with role=dialog when isOpen is true", () => {
    render(<ConfirmDialog {...baseProps} />);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("has aria-modal=true", () => {
    render(<ConfirmDialog {...baseProps} />);
    expect(screen.getByRole("dialog")).toHaveAttribute("aria-modal", "true");
  });

  it("has aria-labelledby pointing to title", () => {
    render(<ConfirmDialog {...baseProps} />);
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-labelledby", "confirm-dialog-title");
    expect(screen.getByText("Delete item?")).toHaveAttribute("id", "confirm-dialog-title");
  });

  it("has aria-describedby pointing to message", () => {
    render(<ConfirmDialog {...baseProps} />);
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-describedby", "confirm-dialog-desc");
    expect(screen.getByText("This action cannot be undone.")).toHaveAttribute("id", "confirm-dialog-desc");
  });

  it("calls onClose when Cancel is clicked", () => {
    const onClose = vi.fn();
    render(<ConfirmDialog {...baseProps} onClose={onClose} />);
    fireEvent.click(screen.getByText("Cancel"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("calls onConfirm when confirm button is clicked", async () => {
    const onConfirm = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();
    render(<ConfirmDialog {...baseProps} onConfirm={onConfirm} onClose={onClose} />);
    fireEvent.click(screen.getByText("Delete"));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("calls onClose when Escape key is pressed", () => {
    const onClose = vi.fn();
    render(<ConfirmDialog {...baseProps} onClose={onClose} />);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  // --- #190: the dialog traps Tab and restores focus to the trigger ---
  it("keeps Tab focus inside the dialog", () => {
    render(<ConfirmDialog {...baseProps} />);
    const dialog = screen.getByRole("dialog");
    const confirm = screen.getByText("Delete");

    confirm.focus();
    expect(document.activeElement).toBe(confirm);
    fireEvent.keyDown(document, { key: "Tab" });
    // Focus wrapped back to the first control instead of escaping the dialog.
    expect(document.activeElement).toBe(screen.getByText("Cancel"));
    expect(dialog.contains(document.activeElement)).toBe(true);
  });

  it("restores focus to the trigger when it closes", () => {
    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    trigger.focus();

    const { rerender } = render(<ConfirmDialog {...baseProps} />);
    expect(document.activeElement).not.toBe(trigger);

    rerender(<ConfirmDialog {...baseProps} isOpen={false} />);
    expect(document.activeElement).toBe(trigger);
    trigger.remove();
  });
});
