import { expect, test, type Page } from "@playwright/test";

/**
 * Real-browser sign-in loop: the #286 regression ("after signing up I can't
 * sign in") was a redirect-loop class of failure (empty Keycloak redirect
 * URIs + issuer split-horizon) that only a real browser following the full
 * /setup -> /signin -> Keycloak -> dashboard chain can catch.
 *
 * Modes:
 *  - FRESH (CI, default): no E2E credentials — drive the first-run setup
 *    wizard on a fresh stack, then sign in with the admin just created.
 *  - EXISTING (local proof): E2E_USERNAME/E2E_PASSWORD set — skip setup,
 *    start at /signin. A bare Keycloak user may not be provisioned in the
 *    backend, so the final assertion is the regression signal itself:
 *    back on the app origin, off the Keycloak host, not bounced to
 *    /signin?error=, with an Auth.js session cookie present.
 */

const BASE_URL = process.env.E2E_BASE_URL ?? "http://localhost:3000";
const KC_URL = process.env.E2E_KC_URL ?? "http://localhost:8081";
const appOrigin = new URL(BASE_URL).origin;
const kcHost = new URL(KC_URL).host;

const existingUser = process.env.E2E_USERNAME;
const existingPassword = process.env.E2E_PASSWORD;
const freshMode = !existingUser;

// >=12 chars with upper/lower/digit/symbol: satisfies both the setup form
// (min 8) and the realm password policy. Made unique per run so a re-run
// against a non-fresh stack fails loudly instead of reusing an old admin.
const stamp = Date.now().toString(36);
const adminEmail = `e2e-admin-${stamp}@example.com`;
const adminPassword = `E2e!${stamp}Pass9`;

const username = existingUser ?? adminEmail;
const password = existingPassword ?? adminPassword;

function logStep(page: Page, step: string) {
  // URL chain in the log: the first thing you need when a redirect loop
  // regression shows up; the retained trace has the rest.
  console.log(`[signin-e2e] ${step}: ${page.url()}`);
}

test("browser sign-in loop completes (setup -> Keycloak -> app)", async ({ page }) => {
  if (freshMode) {
    await page.goto("/");
    await expect(page, "fresh stack should redirect / to /setup").toHaveURL(/\/setup/);
    logStep(page, "setup wizard");

    await page.getByLabel("Organization name").fill("E2E Org");
    await page.getByLabel("Admin email").fill(adminEmail);
    await page.getByLabel("Password", { exact: true }).fill(adminPassword);
    await page.getByLabel("Confirm password").fill(adminPassword);
    // Setup token left blank: inert on the localhost stack (see setup form).
    await page.getByRole("button", { name: "Set up FreeCloud" }).click();

    await expect(page, "setup should land on /signin").toHaveURL(/\/signin/);
    logStep(page, "after setup");
  } else {
    await page.goto("/signin");
    logStep(page, "signin (existing mode)");
  }

  await expect(page.getByRole("button", { name: "Sign in with Keycloak" })).toBeVisible();
  await page.getByRole("button", { name: "Sign in with Keycloak" }).click();

  // Keycloak login page (default theme: stable #username/#password/#kc-login).
  await page.waitForURL((url) => url.host === kcHost, { timeout: 60_000 });
  logStep(page, "keycloak login");
  await expect(page.locator("#username")).toBeVisible();
  await expect(page.locator("#password")).toBeVisible();

  await page.locator("#username").fill(username);
  await page.locator("#password").fill(password);
  await page.locator("#kc-login").click();

  // The #286 loop: the browser must come BACK to the app origin and stay
  // there — not bounce between /signin and Keycloak.
  await page.waitForURL((url) => url.origin === appOrigin, { timeout: 60_000 });
  await expect
    .poll(() => page.url(), { message: "should settle on the app, not /signin?error=" })
    .not.toContain("/signin?error=");
  await expect
    .poll(() => new URL(page.url()).host, { message: "should not return to Keycloak host" })
    .toBe(new URL(BASE_URL).host);
  logStep(page, "returned to app");

  const cookies = await page.context().cookies();
  const sessionCookie = cookies.find((c) => c.name.includes("authjs.session-token"));
  console.log(`[signin-e2e] session cookie: ${sessionCookie?.name ?? "(none found)"} final URL: ${page.url()}`);
  expect(sessionCookie, "Auth.js session cookie must be set after the loop").toBeTruthy();

  if (freshMode) {
    // Provisioned admin reaches the dashboard shell: the Sidebar (root
    // layout) renders a <nav> whose first link is Dashboard. The sidebar
    // is suppressed on /signin, /setup and /access-blocked, so its
    // presence proves an authenticated app page.
    await expect(page.getByRole("navigation")).toBeVisible();
    await expect(page.getByRole("link", { name: "Dashboard" })).toBeVisible();
    logStep(page, "dashboard shell visible");
  }
});
