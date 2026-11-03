package flows

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitallysavvy/go-ai/pkg/provider"
	"github.com/digitallysavvy/go-ai/pkg/provider/types"
	"github.com/digitallysavvy/go-ai/pkg/testutil"
	"github.com/eriicafes/go-workflow"
	localworld "github.com/eriicafes/go-workflow/worlds/local"
	"github.com/eriicafes/got"
	"github.com/redis/go-redis/v9"

	"github.com/eriicafes/loveloop/lib/store"
	"github.com/eriicafes/loveloop/lib/telegram"
	"github.com/eriicafes/loveloop/workflows"
)

const testChatID = 42

// conversationTest runs LoveLoop's workflows on a local World with model, a
// store on REDIS_URL and a fake Telegram. It skips when REDIS_URL is not set.
type conversationTest struct {
	ctx   context.Context
	store *store.Store
	mu    sync.Mutex
	sent  []string
}

func newConversationTest(t *testing.T, model provider.LanguageModel) *conversationTest {
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
		keys, _ := rdb.Keys(context.Background(), prefix+":*").Result()
		if len(keys) > 0 {
			rdb.Del(context.Background(), keys...)
		}
		rdb.Close()
	})
	ct := &conversationTest{store: store.New(rdb, prefix)}
	if _, _, err := ct.store.EnsureUser(t.Context(), testChatID, "Ada", time.Now()); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var params map[string]any
		_ = json.NewDecoder(r.Body).Decode(&params)
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			ct.mu.Lock()
			ct.sent = append(ct.sent, params["text"].(string))
			ct.mu.Unlock()
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
	}))
	t.Cleanup(server.Close)
	bot := telegram.New("test")
	bot.BaseURL = server.URL

	c := got.New()
	got.Mock2(c, GetFlows, &Flows{Store: ct.store, Bot: bot, Model: model}, nil)
	runners, err := GetRunners.From(c)
	if err != nil {
		t.Fatal(err)
	}
	world, err := localworld.Open(localworld.Config{Dir: t.TempDir()}, runners)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { world.Close() })
	if err := world.Prepare(t.Context()); err != nil {
		t.Fatal(err)
	}
	ct.ctx = workflow.NewContext(t.Context(), workflow.NewClient(world))
	return ct
}

func (ct *conversationTest) deliver(t *testing.T, text string) {
	t.Helper()
	if err := workflows.Deliver(ct.ctx, ct.store, testChatID, store.Mail{Text: text}); err != nil {
		t.Fatal(err)
	}
}

func (ct *conversationTest) replies() []string {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return append([]string(nil), ct.sent...)
}

// TestConversationAnswersMailInOrder delivers a message, then two more while
// the first is being answered, and checks that the two wait for that turn
// and are answered together with its context.
func TestConversationAnswersMailInOrder(t *testing.T) {
	// The first model call waits until the test releases it, so the next
	// messages arrive while the first turn is being answered.
	release := make(chan struct{})
	var mu sync.Mutex
	var prompts []string
	model := &testutil.MockLanguageModel{
		ToolSupport: true,
		DoGenerateFunc: func(ctx context.Context, opts *provider.GenerateOptions) (*types.GenerateResult, error) {
			mu.Lock()
			prompts = append(prompts, fmt.Sprintf("%+v", opts.Prompt))
			call := len(prompts)
			mu.Unlock()
			if call == 1 {
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return &types.GenerateResult{Text: fmt.Sprintf("reply %d", call), FinishReason: types.FinishReasonStop}, nil
		},
	}
	ct := newConversationTest(t, model)

	ct.deliver(t, "one")
	waitFor(t, "the first model call", func() bool { mu.Lock(); defer mu.Unlock(); return len(prompts) == 1 })
	ct.deliver(t, "two")
	ct.deliver(t, "three")
	close(release)
	waitFor(t, "two replies", func() bool { return len(ct.replies()) == 2 })
	// Mail is acked after its turn finishes, which is after its reply.
	waitFor(t, "the mailbox to empty", func() bool {
		count, err := ct.store.MailCount(t.Context(), testChatID)
		return err == nil && count == 0
	})

	if replies := ct.replies(); replies[0] != "reply 1" || replies[1] != "reply 2" {
		t.Fatalf("replies = %q", replies)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 2 {
		t.Fatalf("model called %d times, want 2", len(prompts))
	}
	for _, text := range []string{"one", "reply 1", "two", "three"} {
		if !strings.Contains(prompts[1], text) {
			t.Errorf("second turn's prompt lacks %q", text)
		}
	}
}

// TestConversationRunsToolSteps checks that a tool call runs as a step for
// the user whose conversation it is.
func TestConversationRunsToolSteps(t *testing.T) {
	// The first call saves Sarah, and the second replies after the result.
	calls := 0
	model := &testutil.MockLanguageModel{
		ToolSupport: true,
		DoGenerateFunc: func(ctx context.Context, opts *provider.GenerateOptions) (*types.GenerateResult, error) {
			calls++
			if calls == 1 {
				return &types.GenerateResult{
					FinishReason: types.FinishReasonToolCalls,
					ToolCalls: []types.ToolCall{{
						ID:        "call-1",
						ToolName:  "rememberPerson",
						Arguments: map[string]any{"name": "Sarah", "relationship": "girlfriend", "addLikes": []any{"tulips"}},
					}},
				}, nil
			}
			return &types.GenerateResult{Text: "Saved Sarah 💐", FinishReason: types.FinishReasonStop}, nil
		},
	}
	ct := newConversationTest(t, model)

	ct.deliver(t, "my girlfriend Sarah loves tulips")
	waitFor(t, "a reply", func() bool { return len(ct.replies()) == 1 })
	if replies := ct.replies(); replies[0] != "Saved Sarah 💐" {
		t.Fatalf("reply = %q", replies[0])
	}
	person, err := ct.store.Person(t.Context(), testChatID, "sarah")
	if err != nil {
		t.Fatal(err)
	}
	if person.Relationship != "girlfriend" || len(person.Likes) != 1 || person.Likes[0] != "tulips" {
		t.Fatalf("saved person = %+v", person)
	}
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestScheduleReminderLimit checks that a user at the reminder limit gets no
// new reminder, and that the agent hears why.
func TestScheduleReminderLimit(t *testing.T) {
	var mu sync.Mutex
	var prompts []string
	model := &testutil.MockLanguageModel{
		ToolSupport: true,
		DoGenerateFunc: func(ctx context.Context, opts *provider.GenerateOptions) (*types.GenerateResult, error) {
			// JSON shows the tool results, which %+v prints as pointers.
			prompt, err := json.Marshal(opts.Prompt)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			prompts = append(prompts, string(prompt))
			call := len(prompts)
			mu.Unlock()
			if call == 1 {
				at := time.Now().Add(48 * time.Hour).UTC().Format("2006-01-02T15:04")
				return &types.GenerateResult{
					FinishReason: types.FinishReasonToolCalls,
					ToolCalls: []types.ToolCall{{
						ID:        "call-1",
						ToolName:  "scheduleReminder",
						Arguments: map[string]any{"at": at, "about": "call mom"},
					}},
				}, nil
			}
			return &types.GenerateResult{Text: "You're at the limit", FinishReason: types.FinishReasonStop}, nil
		},
	}
	ct := newConversationTest(t, model)
	for i := range maxActiveReminders {
		reminder := store.Reminder{ID: fmt.Sprintf("r%d", i), At: time.Now().Add(time.Hour), About: "existing"}
		if err := ct.store.SaveReminder(t.Context(), testChatID, reminder); err != nil {
			t.Fatal(err)
		}
	}

	ct.deliver(t, "remind me to call mom in two days")
	waitFor(t, "a reply", func() bool { return len(ct.replies()) == 1 })
	if count, err := ct.store.ReminderCount(t.Context(), testChatID); err != nil || count != maxActiveReminders {
		t.Fatalf("reminders = %d, %v", count, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 2 || !strings.Contains(prompts[1], "the most allowed") {
		t.Fatalf("the agent did not hear about the limit: %d calls", len(prompts))
	}
}
