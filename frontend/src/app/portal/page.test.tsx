import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import PortalPage from "./page";
import { portalMyApps, portalMyCompliance, portalMyDevices } from "@/lib/api";
import type { ComplianceResponse } from "@/lib/api";

vi.mock("@/app/providers", () => ({
  useApiReady: () => true,
}));

vi.mock("@/lib/api", () => ({
  portalMyDevices: vi.fn(),
  portalMyApps: vi.fn(),
  portalMyCompliance: vi.fn(),
}));

const portalMyDevicesMock = vi.mocked(portalMyDevices);
const portalMyAppsMock = vi.mocked(portalMyApps);
const portalMyComplianceMock = vi.mocked(portalMyCompliance);

const emptyCompliance = {
  summary: { compliantDevices: 0, totalDevices: 0 },
  devices: [],
} as unknown as ComplianceResponse;

describe("portal page (app/portal)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    portalMyDevicesMock.mockResolvedValue([]);
    portalMyComplianceMock.mockResolvedValue(emptyCompliance);
  });

  it("links app rows with valid https base URLs", async () => {
    portalMyAppsMock.mockResolvedValue([
      { id: "a1", name: "Files", baseUrl: "https://files.example.com/", protocol: "OIDC", enabled: true },
    ]);
    render(<PortalPage />);
    const link = await screen.findByRole("link", { name: "https://files.example.com/" });
    expect(link).toHaveAttribute("href", "https://files.example.com/");
  });

  it("renders rows with dangerous stored URLs as non-clickable text", async () => {
    portalMyAppsMock.mockResolvedValue([
      { id: "a1", name: "Files", baseUrl: "https://files.example.com/", protocol: "OIDC", enabled: true },
      { id: "a2", name: "Evil", baseUrl: "javascript:alert(document.domain)", protocol: "OIDC", enabled: true },
    ]);
    render(<PortalPage />);
    // The safe row still links.
    const link = await screen.findByRole("link", { name: "https://files.example.com/" });
    expect(link).toHaveAttribute("href", "https://files.example.com/");
    // The dangerous row renders its URL as text with no link.
    const text = await screen.findByText("javascript:alert(document.domain)");
    expect(text.tagName).not.toBe("A");
    expect(screen.queryByRole("link", { name: /javascript:/i })).toBeNull();
    // Nothing on the page may carry a javascript: href.
    for (const a of document.querySelectorAll("a[href]")) {
      expect(a.getAttribute("href")).not.toMatch(/javascript:/i);
    }
  });
});
