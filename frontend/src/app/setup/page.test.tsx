import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";

const { pushMock } = vi.hoisted(() => ({ pushMock: vi.fn() }));

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: pushMock }) }));

import SetupPage from "./page";

function fillForm() {
  fireEvent.change(screen.getByLabelText("Organization name"), { target: { value: "Acme" } });
  fireEvent.change(screen.getByLabelText("First name"), { target: { value: "Ada" } });
  fireEvent.change(screen.getByLabelText("Last name"), { target: { value: "Lovelace" } });
  fireEvent.change(screen.getByLabelText("Admin email"), { target: { value: "admin@example.com" } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: "securepass12" } });
  fireEvent.change(screen.getByLabelText("Confirm password"), { target: { value: "securepass12" } });
  fireEvent.change(screen.getByLabelText("Setup token"), { target: { value: "tok-123" } });
}

describe("Setup page setup-token field", () => {
  beforeEach(() => {
    pushMock.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders an associated password-typed setup token field with the secrets hint", () => {
    render(<SetupPage />);
    const field = screen.getByLabelText("Setup token");
    expect(field).toBeInTheDocument();
    expect(field).toHaveAttribute("type", "password");
    expect(field).toHaveAttribute("autocomplete", "off");
    expect(screen.getByText(/SETUP_TOKEN/)).toBeInTheDocument();
  });

  it("includes setupToken in the POST body and redirects on success", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ status: 201, ok: true, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    render(<SetupPage />);
    fillForm();
    fireEvent.click(screen.getByRole("button", { name: /set up freecloud/i }));

    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/signin"));
    const body = JSON.parse(fetchMock.mock.calls[0][1].body as string);
    expect(body.setupToken).toBe("tok-123");
  });

  it("renders required first/last name fields with stable ids for the browser e2e", () => {
    render(<SetupPage />);
    const firstName = screen.getByLabelText("First name");
    const lastName = screen.getByLabelText("Last name");
    expect(firstName).toHaveAttribute("id", "adminFirstName");
    expect(lastName).toHaveAttribute("id", "adminLastName");
    expect(firstName).toBeRequired();
    expect(lastName).toBeRequired();
  });

  it("includes adminFirstName/adminLastName in the POST body", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ status: 201, ok: true, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    render(<SetupPage />);
    fillForm();
    fireEvent.click(screen.getByRole("button", { name: /set up freecloud/i }));

    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/signin"));
    const body = JSON.parse(fetchMock.mock.calls[0][1].body as string);
    expect(body.adminFirstName).toBe("Ada");
    expect(body.adminLastName).toBe("Lovelace");
  });

  it("maps a 403 to a setup-token field error", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ status: 403, ok: false, json: async () => ({ error: "invalid setup token" }) });
    vi.stubGlobal("fetch", fetchMock);
    render(<SetupPage />);
    fillForm();
    fireEvent.click(screen.getByRole("button", { name: /set up freecloud/i }));

    expect(await screen.findByText(/Invalid setup token/)).toBeInTheDocument();
    expect(pushMock).not.toHaveBeenCalled();
  });
});
