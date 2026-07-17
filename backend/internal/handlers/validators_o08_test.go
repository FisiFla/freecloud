package handlers
import ("testing"; "time")
func TestO08_MintDeviceCookieValue_RejectsPathHostID(t *testing.T) {
	_, err := MintDeviceCookieValue("../etc/passwd", "secret", time.Now())
	if err == nil { t.Fatal("expected error for path host id") }
}
func TestO08_MintDeviceCookieValue_OK(t *testing.T) {
	v, err := MintDeviceCookieValue("host-1", "secret", time.Now())
	if err != nil || v == "" { t.Fatalf("ok mint: %v %q", err, v) }
}
