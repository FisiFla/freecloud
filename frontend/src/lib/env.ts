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
 * isProduction exposes the computed flag for consumers that need to branch.
 */
export function isProduction(): boolean {
  return isProd;
}
