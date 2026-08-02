package main

import (
	"context"
	"testing"
)

func TestCleaningAllowed(t *testing.T) {
	app := NewApp()
	app.enabled = true
	if !app.cleaningAllowed(context.Background()) {
		t.Fatal("cleaning should be allowed while the guardian is enabled")
	}

	app.enabled = false
	if app.cleaningAllowed(context.Background()) {
		t.Fatal("cleaning should stop after the guardian is disabled")
	}

	app.enabled = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if app.cleaningAllowed(ctx) {
		t.Fatal("cleaning should stop after the guardian context is cancelled")
	}
}
