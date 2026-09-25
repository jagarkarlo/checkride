package main

import (
	"strings"
	"testing"
)

func TestRunReturnsBindError(t *testing.T) {
	t.Setenv("CHECKRIDE_ADDR", "invalid-address")

	err := run()
	if err == nil || !strings.Contains(err.Error(), "listen on \"invalid-address\"") {
		t.Fatalf("run() error = %v, want a bind error", err)
	}
}
