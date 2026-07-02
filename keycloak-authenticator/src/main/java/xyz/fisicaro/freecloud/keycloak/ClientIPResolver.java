package xyz.fisicaro.freecloud.keycloak;

/**
 * Resolves the "real" client IP address for network/geo access-condition
 * evaluation (A3).
 *
 * <p>By default (trustProxy=false) this ALWAYS returns the direct TCP peer
 * address (the socket that connected to Keycloak) and NEVER looks at the
 * client-supplied {@code X-Forwarded-For} header — a direct caller can put
 * anything in that header, so trusting it unconditionally would let anyone
 * forge their client IP and bypass network/geo conditions entirely.
 *
 * <p>When trustProxy=true (operator has confirmed Keycloak sits behind
 * exactly one reverse proxy that they control), the resolver reads
 * {@code X-Forwarded-For} and takes the RIGHTMOST entry — not the leftmost.
 * Reverse proxies (nginx {@code $proxy_add_x_forwarded_for}, Caddy's default
 * forwarding, HAProxy, etc.) by convention APPEND the connecting peer's
 * address to any existing header rather than replacing it, so:
 *
 * <pre>
 *   X-Forwarded-For: &lt;attacker-forged-value&gt;, &lt;real-proxy-peer-address&gt;
 * </pre>
 *
 * Only the last hop is guaranteed to have been set by infrastructure this
 * operator controls; everything to the left of it is attacker-controlled
 * input carried through unmodified. Taking the leftmost entry (the common
 * mistake) would let a direct-to-proxy attacker forge an arbitrary "first"
 * IP. This is "first-untrusted-from-the-right" collapsed to the single-proxy
 * case: with exactly one trusted hop, the single rightmost entry is that
 * hop's contribution and everything else is untrusted.
 *
 * <p>If trustProxy=true but the header is absent/empty/unparseable, this
 * falls back to the direct peer address rather than failing — the header
 * missing just means no proxy appended anything (e.g. the health check
 * hitting Keycloak directly), not an attack.
 */
final class ClientIPResolver {

    private ClientIPResolver() {
    }

    /**
     * @param remoteAddr the direct TCP peer address (e.g. from
     *                    {@code AuthenticationFlowContext.getConnection().getRemoteAddr()}).
     * @param forwardedFor the raw {@code X-Forwarded-For} header value, or
     *                      {@code null}/empty if absent.
     * @param trustProxy whether to honour {@code forwardedFor} at all.
     * @return the resolved client IP to use for network/geo conditions.
     */
    static String resolve(String remoteAddr, String forwardedFor, boolean trustProxy) {
        String direct = remoteAddr != null ? remoteAddr.trim() : "";

        if (!trustProxy) {
            // Never even parse the header when the flag is off — a direct
            // (non-proxied) caller's X-Forwarded-For is pure client input.
            return direct;
        }

        if (forwardedFor == null || forwardedFor.isBlank()) {
            return direct;
        }

        // String.split(regex) with the default limit=0 drops trailing empty
        // strings, so an all-separator header (e.g. ",,") yields a zero-length
        // array here, not an array of empty strings — guard both cases.
        String[] hops = forwardedFor.split(",");
        if (hops.length == 0) {
            return direct;
        }
        String rightmost = hops[hops.length - 1].trim();
        if (rightmost.isEmpty()) {
            return direct;
        }
        return rightmost;
    }
}
