package store

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/eriicafes/go-workflow/workflowai"
	"github.com/redis/go-redis/v9"
)

// testStore returns a Store on REDIS_URL under a prefix of its own, and
// skips the test when REDIS_URL is not set.
func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL is not set")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(options)
	prefix := fmt.Sprintf("loveloop-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		keys, _ := rdb.Keys(t.Context(), prefix+":*").Result()
		if len(keys) > 0 {
			rdb.Del(t.Context(), keys...)
		}
		rdb.Close()
	})
	return New(rdb, prefix)
}

func text(role, text string) workflowai.ModelMessage {
	return workflowai.ModelMessage{Role: role, Content: []workflowai.ContentPart{{Type: workflowai.PartText, Text: text}}}
}

func TestCompactHistory(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	messages := []workflowai.ModelMessage{
		text(workflowai.RoleUser, "one"), text(workflowai.RoleAssistant, "1"),
		text(workflowai.RoleUser, "two"), text(workflowai.RoleAssistant, "2"),
	}
	if err := s.AppendHistory(ctx, 1, messages); err != nil {
		t.Fatal(err)
	}
	history, err := s.FullHistory(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}

	// A compaction planned against a different start of the history
	// changes nothing.
	ok, err := s.CompactHistory(ctx, 1, 2, text(workflowai.RoleUser, "zero"), Summary{Text: "stale"})
	if err != nil || ok {
		t.Fatalf("stale compaction: %v, %v", ok, err)
	}

	ok, err = s.CompactHistory(ctx, 1, 2, history[0], Summary{Text: "said one"})
	if err != nil || !ok {
		t.Fatalf("compaction: %v, %v", ok, err)
	}
	left, err := s.FullHistory(ctx, 1)
	if err != nil || len(left) != 2 || left[0].Content[0].Text != "two" {
		t.Fatalf("history after compaction: %+v, %v", left, err)
	}
	if summary, err := s.Summary(ctx, 1); err != nil || summary.Text != "said one" {
		t.Fatalf("summary: %+v, %v", summary, err)
	}

	// Compacting again with the first message already gone does nothing.
	if ok, err := s.CompactHistory(ctx, 1, 2, history[0], Summary{Text: "again"}); err != nil || ok {
		t.Fatalf("repeated compaction: %v, %v", ok, err)
	}

	if err := s.ClearHistory(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if summary, err := s.Summary(ctx, 1); err != nil || summary.Text != "" {
		t.Fatalf("summary after reset: %+v, %v", summary, err)
	}
}
