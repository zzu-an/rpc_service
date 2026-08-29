package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerRejectsMissingConfig(t *testing.T) {
	original := *configFile
	t.Cleanup(func() { *configFile = original })
	*configFile = filepath.Join(t.TempDir(), "missing.yaml")
	if err := run(); err == nil {
		t.Fatal("run() error = nil")
	}
	_ = os.Getenv("PATH")
}
