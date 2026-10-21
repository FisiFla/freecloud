import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import SlideOver from "./SlideOver";

describe("SlideOver", () => {
  it("renders panel with role=dialog when isOpen is true", () => {
    render(
      <SlideOver isOpen={true} onClose={vi.fn()} title="Test Panel">
        <p>Content</p>
      </SlideOver>
    );
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("has aria-modal=true when open", () => {
    render(
      <SlideOver isOpen={true} onClose={vi.fn()} title="Test Panel">
        <p>Content</p>
      </SlideOver>
    );
    expect(screen.getByRole("dialog")).toHaveAttribute("aria-modal", "true");
  });

  it("has aria-labelledby pointing to title", () => {
    render(
      <SlideOver isOpen={true} onClose={vi.fn()} title="Test Panel">
        <p>Content</p>
      </SlideOver>
    );
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-labelledby", "slide-over-title");
    expect(screen.getByText("Test Panel")).toHaveAttribute("id", "slide-over-title");
  });

  it("calls onClose when Escape key is pressed", () => {
    const onClose = vi.fn();
    render(
      <SlideOver isOpen={true} onClose={onClose} title="Test Panel">
        <p>Content</p>
      </SlideOver>
    );
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("calls onClose when Close button is clicked", () => {
    const onClose = vi.fn();
    render(
      <SlideOver isOpen={true} onClose={onClose} title="Test Panel">
        <p>Content</p>
      </SlideOver>
    );
    fireEvent.click(screen.getByLabelText("Close panel"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  // --- #190: focus trap, focus restore and scroll lock ---
  it("keeps Tab focus inside the dialog", () => {
    render(
      <SlideOver isOpen={true} onClose={vi.fn()} title="Test Panel">
        <button type="button">Inside action</button>
      </SlideOver>
    );
    const dialog = screen.getByRole("dialog");
    const inside = screen.getByText("Inside action");
    const close = screen.getByLabelText("Close panel");

    // Focus the last focusable element in the panel and press Tab: focus
    // must wrap back to the first one instead of escaping to the page.
    inside.focus();
    expect(document.activeElement).toBe(inside);
    fireEvent.keyDown(document, { key: "Tab" });
    expect(document.activeElement).toBe(close);
    expect(dialog.contains(document.activeElement)).toBe(true);
  });

  it("wraps backwards with Shift+Tab", () => {
    render(
      <SlideOver isOpen={true} onClose={vi.fn()} title="Test Panel">
        <button type="button">Inside action</button>
      </SlideOver>
    );
    const close = screen.getByLabelText("Close panel");
    close.focus();
    fireEvent.keyDown(document, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(screen.getByText("Inside action"));
  });

  it("restores focus to the trigger when the panel closes", () => {
    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    trigger.focus();

    const { rerender } = render(
      <SlideOver isOpen={true} onClose={vi.fn()} title="Test Panel">
        <p>Content</p>
      </SlideOver>
    );
    // Focus moved into the panel.
    expect(document.activeElement).not.toBe(trigger);

    rerender(
      <SlideOver isOpen={false} onClose={vi.fn()} title="Test Panel">
        <p>Content</p>
      </SlideOver>
    );
    expect(document.activeElement).toBe(trigger);
    trigger.remove();
  });

  it("locks body scrolling while open and restores it on close", () => {
    const { rerender } = render(
      <SlideOver isOpen={true} onClose={vi.fn()} title="Test Panel">
        <p>Content</p>
      </SlideOver>
    );
    expect(document.body.style.overflow).toBe("hidden");
    rerender(
      <SlideOver isOpen={false} onClose={vi.fn()} title="Test Panel">
        <p>Content</p>
      </SlideOver>
    );
    expect(document.body.style.overflow).not.toBe("hidden");
  });

  it("marks the backdrop as decorative", () => {
    const { container } = render(
      <SlideOver isOpen={true} onClose={vi.fn()} title="Test Panel">
        <p>Content</p>
      </SlideOver>
    );
    const backdrop = container.firstElementChild as HTMLElement;
    expect(backdrop).toHaveAttribute("aria-hidden", "true");
  });

  it("keeps the closed panel out of the tab order", () => {
    const { rerender } = render(
      <SlideOver isOpen={true} onClose={vi.fn()} title="Test Panel">
        <p>Content</p>
      </SlideOver>
    );
    expect(screen.getByRole("dialog")).not.toHaveAttribute("inert");
    rerender(
      <SlideOver isOpen={false} onClose={vi.fn()} title="Test Panel">
        <p>Content</p>
      </SlideOver>
    );
    expect(screen.getByRole("dialog")).toHaveAttribute("inert");
  });
});
