package main

import "testing"

func TestRunRejectsMissingMode(t *testing.T) {
	if got := run(nil); got != 2 {
		t.Fatalf("run(nil) = %d, want 2", got)
	}
}

func TestRunRejectsUpstreamCombinedWithPath(t *testing.T) {
	if got := run([]string{"--upstream", "http://127.0.0.1:8089", "--path", t.TempDir()}); got != 2 {
		t.Fatalf("run(upstream with path) = %d, want 2", got)
	}
}

func TestRunRejectsUpstreamCombinedWithDriver(t *testing.T) {
	if got := run([]string{"--upstream", "http://127.0.0.1:8089", "--driver", "leveldb"}); got != 2 {
		t.Fatalf("run(upstream with driver) = %d, want 2", got)
	}
}

func TestRunFailsFastOnUnreachableUpstream(t *testing.T) {
	// Port 1 refuses immediately; the command must fail before serving.
	if got := run([]string{"--upstream", "http://127.0.0.1:1", "--listen", "127.0.0.1:0"}); got != 1 {
		t.Fatalf("run(unreachable upstream) = %d, want 1", got)
	}
}

func TestRunRejectsBadUpstreamDriver(t *testing.T) {
	if got := run([]string{"--upstream", "http://127.0.0.1:1", "--upstream-driver", "not a driver"}); got != 2 {
		t.Fatalf("run(bad upstream driver) = %d, want 2", got)
	}
}
