/**
 * Nonce-based CSP builder. Kept dependency-free so it can be unit-tested in a
 * plain node environment (proxy.ts imports next-auth, which needs the Next
 * server runtime). See src/proxy.ts for where the nonce is generated.
 */

/**
 * buildCsp returns the Content-Security-Policy header value.
 *
 * A fresh nonce is generated per request (src/proxy.ts) and forwarded to the
 * layout on the request headers; layout.tsx applies it to the app's single
 * inline script (dark-mode flash prevention). 'unsafe-inline'/'unsafe-eval'
 * are dropped in production. In development, Next.js HMR requires 'unsafe-eval',
 * so a relaxed CSP is used only outside production.
 *
 * connect-src is 'self' in production: the browser never talks to the Go
 * backend or Keycloak directly — every call goes through the same-origin BFF
 * proxy (app/api/v1/[...path]/route.ts) and the Keycloak sign-in is a
 * top-level navigation, not a fetch. The wildcard 'https:' scheme source was
 * removed because it let any surviving XSS exfiltrate to an arbitrary host.
 * The localhost entries are development-only (HMR websocket + local backend).
 */
export function buildCsp(nonce: string): string {
  if (process.env.NODE_ENV === "production") {
    return [
      "default-src 'self'",
      `script-src 'self' 'nonce-${nonce}'`,
      "style-src 'self' 'unsafe-inline'",
      "img-src 'self' data: blob:",
      "font-src 'self' data:",
      "connect-src 'self'",
      "object-src 'none'",
      "frame-ancestors 'none'",
      "base-uri 'self'",
      "form-action 'self'",
      "upgrade-insecure-requests",
    ].join("; ");
  }
  return [
    "default-src 'self'",
    "script-src 'self' 'unsafe-inline' 'unsafe-eval'",
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self' data:",
    "connect-src 'self' https: http://localhost:8080 http://localhost:8081",
    "object-src 'none'",
    "frame-ancestors 'none'",
    "base-uri 'self'",
    "form-action 'self'",
  ].join("; ");
}
