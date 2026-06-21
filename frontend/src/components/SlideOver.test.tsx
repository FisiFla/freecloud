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
});
