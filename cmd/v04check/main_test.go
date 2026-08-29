package main

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestCalculateLag(t *testing.T) {
	for _, test := range []struct{ committed, end, want int64 }{{3, 10, 7}, {-1, 4, 4}, {10, 10, 0}, {11, 10, 0}} {
		if got := calculateLag(test.committed, test.end); got != test.want {
			t.Fatalf("calculateLag(%d,%d)=%d want %d", test.committed, test.end, got, test.want)
		}
	}
}

func TestRunRejectsMissingConfig(t *testing.T) {
	original := *configFile
	t.Cleanup(func() { *configFile = original })
	*configFile = filepath.Join(t.TempDir(), "missing.yaml")
	if err := run(&bytes.Buffer{}); err == nil {
		t.Fatal("run() error = nil")
	}
}
