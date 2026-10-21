import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";

const { headersMock } = vi.hoisted(() => ({ headersMock: vi.fn() }));

vi.mock("next/headers", () => ({ headers: headersMock }));
vi.mock("next/font/google", () => ({
  Inter: () => ({ className: "inter", variable: "--font-inter" }),
}));
vi.mock("@/components/Sidebar", () => ({ default: () => null }));
vi.mock("./providers", () => ({
  Providers: ({ children }: { children: React.ReactNode }) => children,
}));

import RootLayout from "./layout";

describe("root layout CSP nonce", () => {
  beforeEach(() => {
    headersMock.mockReset();
  });

  it("applies the nonce from the request headers to the inline dark-mode script", async () => {
    headersMock.mockResolvedValue(new Headers({ "x-nonce": "test-nonce-123" }));
    const element = await RootLayout({ children: <p>hello</p> });
    const html = renderToStaticMarkup(element);
    expect(html).toContain('nonce="test-nonce-123"');
  });

  it("renders the inline script without a nonce when the header is absent", async () => {
    headersMock.mockResolvedValue(new Headers());
    const element = await RootLayout({ children: <p>hello</p> });
    const html = renderToStaticMarkup(element);
    expect(html).toContain("<script");
    expect(html).not.toContain("nonce=");
  });
});
