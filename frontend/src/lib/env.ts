/**
 * Typed environment-variable helpers with a fail-closed production mode.
 *
 * Mirrors the backend's config.Validate(): in development, sensible defaults
 * are accepted; in production, required secrets must be set explicitly or the
 * app throws at runtime rather than silently running misconfigured.
 *
 * NOTE: `next build` imports modules to collect page data under
 * NODE_ENV=production, but secrets aren't available at build time. We detect
 * the build phase via NEXT_PHASE and skip validation then, so validation only
 * fires at actual request-serving runtime.
 */

const isProd = process.env.NODE_ENV === "production";
// Next.js sets NEXT_PHASE to PhaseProductionBuilding during `next build`.
const isBuildPhase = process.env.NEXT_PHASE === "phase-production-build";

/**
 * requiredEnv throws if the named variable is unset/empty. Skipped during
 * `next build` (always enforced at runtime).
 */
export function requiredEnv(name: string): string {
  const v = process.env[name];
  if (!v && !isBuildPhase) {
    throw new Error(`Missing required environment variable: ${name}`);
  }
  return v ?? "";
}

/**
 * requiredProdEnv returns the variable, throwing in production (at runtime) if
 * it is unset. In development the optional `fallback` is used and a warning is
 * logged. Skipped during `next build`.
 */
export function requiredProdEnv(name: string, fallback: string): string {
  const v = process.env[name];
  if (v) return v;
  if (isProd && !isBuildPhase) {
    throw new Error(`Missing required environment variable in production: ${name}`);
  }
  if (!isProd) {
    // eslint-disable-next-line no-console
    console.warn(`[dev] ${name} not set, using fallback. Do NOT use in production.`);
  }
  return fallback;
}

/**
 * Well-known placeholder secret values shipped in the example env files. They
 * must never be the live value in production.
 */
const INSECURE_PLACEHOLDERS = new Set([
  "change-me-to-a-random-string",
  "change-me",
  "secret",
]);

/**
 * Minimum accepted secret length in production. Short secrets fall to brute
 * force even when they are not on the placeholder list; 32 characters keeps
 * the check well below what the generator produces (generate.sh writes 64
 * hex chars for AUTH_SECRET) while rejecting weak hand-picked values.
 */
export const MIN_SECRET_LENGTH = 32;

/**
 * rejectInsecureInProd throws (at runtime, in production) if a secret is still
 * set to a well-known placeholder value or is shorter than MIN_SECRET_LENGTH.
 * In development it only warns. Skipped during `next build`.
 */
export function rejectInsecureInProd(name: string, value: string): void {
  if (isBuildPhase) return;
  if (INSECURE_PLACEHOLDERS.has(value)) {
    if (isProd) {
      throw new Error(
        `${name} is set to a well-known placeholder; generate a real secret (e.g. \`openssl rand -base64 33\`).`,
      );
    }
    // eslint-disable-next-line no-console
    console.warn(`[dev] ${name} is the example placeholder — fine locally, NEVER in production.`);
    return;
  }
  if (value.length < MIN_SECRET_LENGTH) {
    if (isProd) {
      throw new Error(
        `${name} must be at least ${MIN_SECRET_LENGTH} characters in production; generate a real secret (e.g. \`openssl rand -base64 33\`).`,
      );
    }
    // eslint-disable-next-line no-console
    console.warn(`[dev] ${name} is shorter than ${MIN_SECRET_LENGTH} characters — fine locally, NEVER in production.`);
  }
}

/**
 * isProduction exposes the computed flag for consumers that need to branch.
 */
export function isProduction(): boolean {
  return isProd;
}

/**
 * isLoopbackHost reports whether a URL hostname is a loopback address:
 * localhost, *.localhost, 127/8, or ::1. Shared by the production issuer
 * guard and the safe-external-URL helper.
 */
export function isLoopbackHost(host: string): boolean {
  const h = host.toLowerCase().replace(/\.$/, "");
  return (
    h === "localhost" ||
    h.endsWith(".localhost") ||
    h.startsWith("127.") ||
    h === "::1" ||
    h === "[::1]"
  );
}

/**
 * validateProdIssuer enforces the Keycloak issuer contract at runtime in
 * production: the issuer must be an absolute https URL, so tokens never
 * transit cleartext. A loopback issuer (localhost, *.localhost, 127/8, ::1)
 * only WARNS in production — deliberately not a hard failure — because the
 * prod-smoke stack runs the production build against https://auth.localhost
 * and is indistinguishable from a real deployment by NODE_ENV (the same
 * mechanism every other startup guard uses; see scripts/prod-smoke.sh).
 * Skipped during `next build`, warn-only in development.
 */
export function validateProdIssuer(issuer: string): void {
  if (isBuildPhase) return;
  let url: URL;
  try {
    url = new URL(issuer);
  } catch {
    if (isProd) {
      throw new Error(`AUTH_KEYCLOAK_ISSUER is not a valid absolute URL: ${issuer}`);
    }
    // eslint-disable-next-line no-console
    console.warn("[dev] AUTH_KEYCLOAK_ISSUER is not a valid absolute URL — fine locally, NEVER in production.");
    return;
  }
  if (url.protocol !== "https:") {
    if (isProd) {
      throw new Error(
        `AUTH_KEYCLOAK_ISSUER must use https in production (got ${url.protocol}//${url.host}); tokens must never transit cleartext.`,
      );
    }
    // eslint-disable-next-line no-console
    console.warn("[dev] AUTH_KEYCLOAK_ISSUER is not https — fine locally, NEVER in production.");
    return;
  }
  if (isLoopbackHost(url.hostname)) {
    // eslint-disable-next-line no-console
    console.error(
      `[security] AUTH_KEYCLOAK_ISSUER points at loopback (${url.hostname}) in production. ` +
        "Expected only for the prod-smoke stack (https://*.localhost); a real deployment must use the public Keycloak origin.",
    );
  }
}
