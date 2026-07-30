package main

import "testing"

func TestGetenvFallback(t *testing.T) {
    t.Setenv("COMPOUND_TEST_VALUE", "")
    if got := getenv("COMPOUND_TEST_VALUE", "fallback"); got != "fallback" {
        t.Fatalf("expected fallback, got %q", got)
    }
}
