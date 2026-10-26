package main

import (
	"strings"
	"testing"
	"time"
)

func TestGuardReturnsResult(t *testing.T) {
	g := guardSet{}
	v, err := g.run("ok", time.Second, func() (map[string]interface{}, error) {
		return map[string]interface{}{"a": 1}, nil
	})
	if err != nil || v["a"] != 1 {
		t.Fatalf("unexpected: %v %v", v, err)
	}
}

func TestGuardRecoversPanic(t *testing.T) {
	g := guardSet{}
	_, err := g.run("boom", time.Second, func() (map[string]interface{}, error) {
		var m map[string]int
		m["x"] = 1 // nil map write panics
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("expected a panic error, got %v", err)
	}
	// The guard must be usable again afterwards.
	if _, err := g.run("boom", time.Second, func() (map[string]interface{}, error) { return nil, nil }); err != nil {
		t.Fatalf("guard stuck after panic: %v", err)
	}
}

func TestGuardTimesOutAndSkipsWhileStillRunning(t *testing.T) {
	g := guardSet{}
	release := make(chan struct{})
	_, err := g.run("slow", 20*time.Millisecond, func() (map[string]interface{}, error) {
		<-release
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout, got %v", err)
	}
	// The hung call is still running: the next cycle must not start another.
	if _, err := g.run("slow", time.Second, func() (map[string]interface{}, error) { return nil, nil }); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("expected the cycle to be skipped, got %v", err)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := g.run("slow", time.Second, func() (map[string]interface{}, error) { return nil, nil }); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("guard never recovered after the hung call finished")
}
