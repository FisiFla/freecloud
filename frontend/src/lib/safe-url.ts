/**
 * Validates a stored URL before it becomes a link href.
 *
 * The backend validates app base URLs on write, but the render site must not
 * trust that: a stored `javascript:` URL rendered into an `href` would execute
 * in the dashboard origin on click. This helper allow-lists schemes — https
 * always, http only for loopback hosts outside production (the localhost dev
 * stack serves apps over http) — and returns null for anything else so the
 * caller renders non-clickable text instead.
 */
import { isLoopbackHost, isProduction } from "./env";

// C0 controls, DEL, and C1 controls. The WHATWG URL parser strips tab/newline,
// so `java\tscript:` would otherwise parse as the javascript scheme; raw
// controls can also smuggle bytes past prefix checks. Legitimate URLs never
// contain them (they would be percent-encoded).
const CONTROL_CHARS = /[\u0000-\u001f\u007f-\u009f]/;

export function safeExternalUrl(raw: string | null | undefined): string | null {
  if (!raw) return null;
  const trimmed = raw.trim();
  if (!trimmed || CONTROL_CHARS.test(trimmed)) return null;
  let url: URL;
  try {
    // No base URL: relative and protocol-relative (//host/...) inputs throw.
    url = new URL(trimmed);
  } catch {
    return null;
  }
  if (url.protocol === "https:") return trimmed;
  if (url.protocol === "http:" && !isProduction() && isLoopbackHost(url.hostname)) {
    return trimmed;
  }
  return null;
}
