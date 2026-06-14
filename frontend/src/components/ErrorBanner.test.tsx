import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import ErrorBanner from "./ErrorBanner";

describe("ErrorBanner", () => {
  it("renders the message", () => {
    render(<ErrorBanner message="Something went wrong" />);
    expect(screen.getByText("Something went wrong")).toBeInTheDocument();
  });

  it("renders the title when provided", () => {
    render(<ErrorBanner message="detail" title="Deprovisioning failed" />);
    expect(screen.getByText("Deprovisioning failed")).toBeInTheDocument();
    expect(screen.getByText("detail")).toBeInTheDocument();
  });

  it("does not render a dismiss button when onDismiss is omitted", () => {
    render(<ErrorBanner message="err" />);
    expect(screen.queryByLabelText("Dismiss")).not.toBeInTheDocument();
  });

  it("calls onDismiss when the dismiss button is clicked", () => {
    const onDismiss = vi.fn();
    render(<ErrorBanner message="err" onDismiss={onDismiss} />);
    fireEvent.click(screen.getByLabelText("Dismiss"));
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });
});
