package xyz.fisicaro.freecloud.keycloak;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;

class ClientIPResolverTest {

    @Test
    void trustProxyOff_alwaysReturnsDirectPeer_evenWithForgedHeader() {
        assertEquals("10.0.0.5",
            ClientIPResolver.resolve("10.0.0.5", "6.6.6.6", false));
    }

    @Test
    void trustProxyOff_ignoresHeaderEvenIfAbsent() {
        assertEquals("10.0.0.5",
            ClientIPResolver.resolve("10.0.0.5", null, false));
    }

    @Test
    void trustProxyOn_singleHop_usesThatHop() {
        assertEquals("203.0.113.9",
            ClientIPResolver.resolve("10.0.0.5", "203.0.113.9", true));
    }

    @Test
    void trustProxyOn_multipleHops_usesRightmostNotLeftmost() {
        // Leftmost is attacker-forged input carried through by the proxy;
        // rightmost is the value the trusted proxy itself appended.
        assertEquals("203.0.113.9",
            ClientIPResolver.resolve("10.0.0.5", "6.6.6.6, 203.0.113.9", true));
    }

    @Test
    void trustProxyOn_multipleHops_trimsWhitespace() {
        assertEquals("203.0.113.9",
            ClientIPResolver.resolve("10.0.0.5", "6.6.6.6,   203.0.113.9  ", true));
    }

    @Test
    void trustProxyOn_headerAbsent_fallsBackToDirectPeer() {
        assertEquals("10.0.0.5",
            ClientIPResolver.resolve("10.0.0.5", null, true));
    }

    @Test
    void trustProxyOn_headerBlank_fallsBackToDirectPeer() {
        assertEquals("10.0.0.5",
            ClientIPResolver.resolve("10.0.0.5", "   ", true));
    }

    @Test
    void trustProxyOn_headerTrailingComma_usesLastNonEmptySegment() {
        // Java's String.split(regex) with the default limit=0 drops trailing
        // empty strings, so "203.0.113.9,".split(",") == ["203.0.113.9"] —
        // the rightmost (only) segment is used, not an empty string.
        assertEquals("203.0.113.9",
            ClientIPResolver.resolve("10.0.0.5", "203.0.113.9,", true));
    }

    @Test
    void trustProxyOn_headerAllCommas_fallsBackToDirectPeer() {
        // Java's split(",") on ",," with default limit still yields [] (all
        // segments empty and trailing, all dropped) — no hop to trust.
        assertEquals("10.0.0.5",
            ClientIPResolver.resolve("10.0.0.5", ",,", true));
    }

    @Test
    void trustProxyOn_nullRemoteAddr_returnsEmptyWhenNoHeader() {
        assertEquals("", ClientIPResolver.resolve(null, null, true));
    }
}
