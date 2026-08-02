package main

import (
	"context"
	"encoding/json"
	"strings"
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

func TestEmptyStatusCollectionsSerializeAsArrays(t *testing.T) {
	data, err := json.Marshal(NewApp().GetStatus())
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}

	encoded := string(data)
	if !strings.Contains(encoded, `"processes":[]`) {
		t.Fatalf("empty processes must serialize as an array: %s", encoded)
	}
	if !strings.Contains(encoded, `"history":[]`) {
		t.Fatalf("empty history must serialize as an array: %s", encoded)
	}
}
