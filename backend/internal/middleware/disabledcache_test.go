package middleware

import (
	"testing"
	"time"
)

// TestDisabledCacheEvictsExpiredEntries proves the disabled-user cache does
// not grow by one entry per distinct JWT subject for the lifetime of the
// process (issue #135).
func TestDisabledCacheEvictsExpiredEntries(t *testing.T) {
	am := NewAuthMiddleware("http://localhost:8081", "freecloud", "freecloud-dashboard")

	am.disabledCacheSet("subject-1", false)
	am.disabledCacheSet("subject-2", true)

	am.disabledCacheMu.Lock()
	size := len(am.disabledCache)
	am.disabledCacheMu.Unlock()
	if size != 2 {
		t.Fatalf("expected 2 cached entries, got %d", size)
	}

	// Expire the first entry.
	am.disabledCacheMu.Lock()
	e := am.disabledCache["subject-1"]
	e.expiresAt = time.Now().Add(-time.Second)
	am.disabledCache["subject-1"] = e
	am.disabledCacheMu.Unlock()

	// A lookup of the expired entry must evict it.
	if _, ok := am.disabledCacheGet("subject-1"); ok {
		t.Error("expected the expired entry to be a cache miss")
	}
	am.disabledCacheMu.Lock()
	_, stillThere := am.disabledCache["subject-1"]
	am.disabledCacheMu.Unlock()
	if stillThere {
		t.Error("expected the expired entry to be removed from the cache")
	}
}

// TestDisabledCacheSweepReclaimsExpiredEntries proves that inserting a new
// entry also reclaims the expired ones, so a subject that is never seen
// again does not stay in the cache forever.
func TestDisabledCacheSweepReclaimsExpiredEntries(t *testing.T) {
	am := NewAuthMiddleware("http://localhost:8081", "freecloud", "freecloud-dashboard")

	am.disabledCacheSet("stale-subject", false)

	// Age the entry (both the map entry and its queued record, which is what
	// a subject that has not been seen for longer than the TTL looks like).
	am.disabledCacheMu.Lock()
	e := am.disabledCache["stale-subject"]
	e.expiresAt = time.Now().Add(-time.Second)
	am.disabledCache["stale-subject"] = e
	am.disabledCacheOrder[0].expiresAt = e.expiresAt
	am.disabledCacheMu.Unlock()

	// Inserting another subject must sweep the expired one away.
	am.disabledCacheSet("fresh-subject", false)

	am.disabledCacheMu.Lock()
	_, staleStillThere := am.disabledCache["stale-subject"]
	size := len(am.disabledCache)
	am.disabledCacheMu.Unlock()
	if staleStillThere {
		t.Error("expected the expired entry to be swept on insert")
	}
	if size != 1 {
		t.Errorf("expected only the fresh entry to remain, got %d entries", size)
	}
}

// TestDisabledCacheKeepsRefreshedEntry proves the sweep does not evict an
// entry that was refreshed after the queued record was written.
func TestDisabledCacheKeepsRefreshedEntry(t *testing.T) {
	am := NewAuthMiddleware("http://localhost:8081", "freecloud", "freecloud-dashboard")

	am.disabledCacheSet("subject-1", false)

	// Refresh the entry (as a later request would).
	am.disabledCacheSet("subject-1", true)

	// Age the FIRST queued record so the sweep sees it as expired, while the
	// map still holds the refreshed (live) entry.
	am.disabledCacheMu.Lock()
	am.disabledCacheOrder[0].expiresAt = time.Now().Add(-time.Second)
	am.disabledCacheMu.Unlock()

	am.disabledCacheSet("other-subject", false)

	am.disabledCacheMu.Lock()
	e, ok := am.disabledCache["subject-1"]
	am.disabledCacheMu.Unlock()
	if !ok {
		t.Fatal("expected the refreshed entry to survive the sweep")
	}
	if !e.disabled {
		t.Error("expected the refreshed value to survive the sweep")
	}
}
