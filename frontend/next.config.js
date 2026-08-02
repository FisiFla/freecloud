/** @type {import('next').NextConfig} */

// Baseline security headers applied to every response. The CSP is NOT set
// here — src/proxy.ts (middleware) owns it so it can inject a per-request
// nonce and drop 'unsafe-inline'/'unsafe-eval' in production. The remaining
// headers below are static and safe to apply to all routes. `connect-src`
// lives inside the middleware CSP so the browser can reach the backend API
// and Keycloak over TLS; localhost is permitted for dev.
const securityHeaders = [
  { key: "X-Frame-Options", value: "DENY" },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  {
    key: "Strict-Transport-Security",
    value: "max-age=63072000; includeSubDomains; preload",
  },
  { key: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=()" },
];

const nextConfig = {
  output: "standalone",
  async headers() {
    return [{ source: "/:path*", headers: securityHeaders }];
  },
};

module.exports = nextConfig;
