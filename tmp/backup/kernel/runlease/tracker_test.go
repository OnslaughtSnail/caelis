package runlease

import "testing"

func TestTrackerAcquireRelease(t *testing.T) {
	tracker := New()
	key := Key("app", "user", "session")
	if !tracker.Acquire(key) {
		t.Fatal("expected first acquire to succeed")
	}
	if tracker.Acquire(key) {
		t.Fatal("expected duplicate acquire to fail")
	}
	if !tracker.Has(key) {
		t.Fatal("expected tracker to report active key")
	}
	tracker.Release(key)
	if tracker.Has(key) {
		t.Fatal("expected key to be released")
	}
	if !tracker.Acquire(key) {
		t.Fatal("expected acquire after release to succeed")
	}
}
