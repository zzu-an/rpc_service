package main

import (
	"bytes"
	"path/filepath"
	"testing"

	redis "github.com/redis/go-redis/v9"
)

func TestSummarizeGroupsSortsAndKeepsBacklogDimensions(t *testing.T) {
	got := summarizeGroups([]redis.XInfoGroup{
		{Name: "z", Consumers: 2, Pending: 3, Lag: 4, LastDeliveredID: "2-0"},
		{Name: "a", Consumers: 1, Pending: 5, Lag: 6, LastDeliveredID: "1-0"},
	})
	if len(got) != 2 || got[0].Name != "a" || got[0].Pending != 5 || got[0].Lag != 6 || got[1].Name != "z" {
		t.Fatalf("summarizeGroups() = %+v", got)
	}
}

func TestRunRejectsMissingItemBeforeOpeningDependencies(t *testing.T) {
	originalItem, originalConfig := *itemID, *configFile
	t.Cleanup(func() { *itemID, *configFile = originalItem, originalConfig })
	*itemID = 0
	*configFile = filepath.Join(t.TempDir(), "missing.yaml")
	if err := run(&bytes.Buffer{}); err == nil {
		t.Fatal("run() error = nil")
	}
}
