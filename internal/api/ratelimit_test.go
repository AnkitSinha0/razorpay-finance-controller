package api

import "testing"

func TestRateLimiter_AllowsUpToCapacityThenBlocks(t *testing.T) {
	rl := newRateLimiter(3, 0) // no refill, so the 4th call must fail
	for i := 0; i < 3; i++ {
		if !rl.Allow("1.2.3.4") {
			t.Fatalf("call %d: want allowed, got blocked", i+1)
		}
	}
	if rl.Allow("1.2.3.4") {
		t.Error("4th call: want blocked, got allowed")
	}
}

func TestRateLimiter_KeysAreIndependent(t *testing.T) {
	rl := newRateLimiter(1, 0)
	if !rl.Allow("a") {
		t.Error("first call for key a should be allowed")
	}
	if !rl.Allow("b") {
		t.Error("a different key should have its own budget")
	}
	if rl.Allow("a") {
		t.Error("key a should now be exhausted")
	}
}

func TestRateLimiter_Refills(t *testing.T) {
	rl := newRateLimiter(1, 1000) // fast refill for a quick test
	if !rl.Allow("x") {
		t.Fatal("first call should be allowed")
	}
	if rl.Allow("x") {
		t.Fatal("immediate second call should be blocked")
	}
	// give the fake clock (real time.Now, but a tiny sleep) room to refill
	for i := 0; i < 1000000; i++ {
		if rl.Allow("x") {
			return
		}
	}
	t.Error("expected the bucket to refill and eventually allow another call")
}
