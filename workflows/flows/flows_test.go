package flows

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eriicafes/go-workflow/workflowai"
	"github.com/eriicafes/got"

	"github.com/eriicafes/loveloop/lib/store"
)

func TestDropPendingApprovals(t *testing.T) {
	history := []workflowai.ModelMessage{
		userMessage("forget Sam"),
		{Role: workflowai.RoleAssistant, Content: []workflowai.ContentPart{
			{Type: workflowai.PartText, Text: "Sure?"},
			{Type: workflowai.PartToolCall, ToolCallID: "c1", ToolName: "forgetPerson"},
			{Type: workflowai.PartToolApprovalRequest, ApprovalID: "approval-c1", ToolCallID: "c1"},
		}},
	}
	if got := pendingApprovals(history); len(got) != 1 || got[0] != "approval-c1" {
		t.Fatalf("pendingApprovals = %v", got)
	}
	if id, ok := findPendingApproval(history, approvalKey("approval-c1")); !ok || id != "approval-c1" {
		t.Fatalf("findPendingApproval = %q, %v", id, ok)
	}
	dropped := dropPendingApprovals(history)
	if len(dropped) != 2 || len(dropped[1].Content) != 1 || dropped[1].Content[0].Type != workflowai.PartText {
		t.Fatalf("dropPendingApprovals = %+v", dropped)
	}
	if len(history[1].Content) != 3 {
		t.Fatal("dropPendingApprovals changed its input")
	}

	// The store holds the request, the answer, and the result after an
	// approval turn.
	answered := append(slices.Clone(history),
		workflowai.ModelMessage{Role: workflowai.RoleTool, Content: []workflowai.ContentPart{
			{Type: workflowai.PartToolApprovalResponse, ApprovalID: "approval-c1", Approved: true},
		}},
		workflowai.ModelMessage{Role: workflowai.RoleTool, Content: []workflowai.ContentPart{
			{Type: workflowai.PartToolResult, ToolCallID: "c1", ToolName: "forgetPerson"},
		}},
	)
	if got := pendingApprovals(answered); len(got) != 0 {
		t.Fatalf("pendingApprovals after answer = %v", got)
	}
	resolved := resolveApprovals(answered)
	if len(resolved) != 3 || resolved[2].Content[0].Type != workflowai.PartToolResult {
		t.Fatalf("resolveApprovals = %+v", resolved)
	}
	for _, part := range resolved[1].Content {
		if part.Type == workflowai.PartToolApprovalRequest {
			t.Fatal("resolveApprovals kept the answered request")
		}
	}
	if len(dropPendingApprovals(resolved)) != 3 {
		t.Fatal("dropPendingApprovals dropped a resolved call")
	}
}

func TestPickTimes(t *testing.T) {
	user := store.User{Timezone: "Africa/Lagos", WakeHour: 9, SleepHour: 21, CheckInsPerDay: 3}
	from := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	times, err := pickTimes(t.Context(), timesInput{User: user, From: from})
	if err != nil {
		t.Fatal(err)
	}
	if len(times) != 3 {
		t.Fatalf("got %d times", len(times))
	}
	for i, at := range times {
		if hour := at.In(user.Location()).Hour(); hour < 9 || hour >= 21 {
			t.Errorf("time %v is outside waking hours", at)
		}
		if at.Before(from) || !at.Before(from.Add(24*time.Hour)) {
			t.Errorf("time %v is outside the day", at)
		}
		if i > 0 && at.Sub(times[i-1]) < 90*time.Minute {
			t.Errorf("times %v and %v are too close", times[i-1], at)
		}
	}
}

func TestAwakeWrapsMidnight(t *testing.T) {
	if !awake(23, 10, 2) || !awake(1, 10, 2) || awake(5, 10, 2) {
		t.Fatal("awake does not wrap past midnight")
	}
}

func TestDaysUntil(t *testing.T) {
	now := time.Date(2026, 12, 30, 15, 0, 0, 0, time.UTC)
	if days, ok := daysUntil("1990-01-02", now); !ok || days != 3 {
		t.Fatalf("daysUntil = %d, %v", days, ok)
	}
	if days, ok := daysUntil("12-30", now); !ok || days != 0 {
		t.Fatalf("daysUntil today = %d, %v", days, ok)
	}
}

func TestMerge(t *testing.T) {
	got := merge([]string{"Tulips", "Jazz"}, []string{"tulips", "sushi", " "}, []string{"jazz"})
	if len(got) != 2 || got[0] != "Tulips" || got[1] != "sushi" {
		t.Fatalf("merge = %v", got)
	}
}

func TestSchemaForDescriptions(t *testing.T) {
	schema := schemaFor[contactInput]()
	properties := schema["properties"].(map[string]any)
	if properties["name"].(map[string]any)["description"] == nil {
		t.Fatalf("name has no description: %v", schema)
	}
	if required, _ := schema["required"].([]string); len(required) != 1 || required[0] != "name" {
		t.Fatalf("required = %v", schema["required"])
	}
}

func TestRunnersRegisterMethods(t *testing.T) {
	c := got.New()
	got.Mock2(c, GetFlows, &Flows{}, nil)
	runners, err := GetRunners.From(c)
	if err != nil {
		t.Fatal(err)
	}
	if runners == nil {
		t.Fatal("no runners")
	}
}

func TestCompactionCut(t *testing.T) {
	var history []workflowai.ModelMessage
	for range 30 {
		history = append(history,
			userMessage("hi"),
			workflowai.ModelMessage{Role: workflowai.RoleAssistant, Content: []workflowai.ContentPart{{Type: workflowai.PartToolCall, ToolCallID: "c", ToolName: "logContact"}}},
			workflowai.ModelMessage{Role: workflowai.RoleTool, Content: []workflowai.ContentPart{{Type: workflowai.PartToolResult, ToolCallID: "c"}}},
		)
	}
	cut := compactionCut(history, 40)
	if cut < len(history)-40 || history[cut].Role != workflowai.RoleUser {
		t.Fatalf("cut %d of %d lands on %s", cut, len(history), history[cut].Role)
	}
	if cut := compactionCut(history[:10], 40); cut != 0 && history[cut].Role != workflowai.RoleUser {
		t.Fatalf("short history cut %d", cut)
	}
	onlyAssistant := []workflowai.ModelMessage{{Role: workflowai.RoleAssistant}, {Role: workflowai.RoleAssistant}}
	if cut := compactionCut(onlyAssistant, 1); cut != 0 {
		t.Fatalf("cut %d without a user message", cut)
	}
}

func TestTranscript(t *testing.T) {
	got := transcript([]workflowai.ModelMessage{
		userMessage("I saw Ada today"),
		{Role: workflowai.RoleAssistant, Content: []workflowai.ContentPart{
			{Type: workflowai.PartToolCall, ToolName: "logContact", Input: []byte(`{"name":"Ada"}`)},
		}},
		{Role: workflowai.RoleTool, Content: []workflowai.ContentPart{{Type: workflowai.PartToolResult}}},
		{Role: workflowai.RoleAssistant, Content: []workflowai.ContentPart{{Type: workflowai.PartText, Text: "Yay!"}}},
		userMessage("[LoveLoop scheduler, not typed by the user] say hi"),
	})
	want := "User: I saw Ada today\n" +
		"LoveLoop used logContact {\"name\":\"Ada\"}\n" +
		"LoveLoop: Yay!\n" +
		"Scheduler: [LoveLoop scheduler, not typed by the user] say hi\n"
	if got != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", got, want)
	}
}

func TestGroupMail(t *testing.T) {
	mails := []store.Mail{
		{Text: "hey"}, {Text: "so about Sarah"}, {Text: "her bday is May 3"},
		{ApprovalKey: "k", Approved: true},
		{Nudge: "say hi"},
		{Text: "thanks"},
	}
	var sizes []int
	for _, group := range groupMail(mails) {
		sizes = append(sizes, len(group))
	}
	if !slices.Equal(sizes, []int{3, 1, 1, 1}) {
		t.Fatalf("group sizes = %v", sizes)
	}
	if groups := groupMail(nil); len(groups) != 0 {
		t.Fatalf("groups of no mail = %v", groups)
	}
}

// TestGatewayAuthReadsCurrentToken checks that each gateway request carries
// the token set when it is sent, so a rotated OIDC token is picked up.
func TestGatewayAuthReadsCurrentToken(t *testing.T) {
	var got []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
	}))
	defer server.Close()
	client := &http.Client{Transport: gatewayAuth{next: http.DefaultTransport}}
	t.Setenv("AI_GATEWAY_API_KEY", "")
	for _, token := range []string{"oidc-1", "oidc-2"} {
		t.Setenv("VERCEL_OIDC_TOKEN", token)
		response, err := client.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	t.Setenv("AI_GATEWAY_API_KEY", "key")
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if want := []string{"Bearer oidc-1", "Bearer oidc-2", "Bearer key"}; !slices.Equal(got, want) {
		t.Fatalf("authorization = %v, want %v", got, want)
	}
}

func TestCheckDraft(t *testing.T) {
	for text, ok := range map[string]bool{
		"Thinking of you babe, dinner at 7:30? :)": true,
		"Miss you already 💞":                       true,
		"Hey love: thinking of you":                false,
		"Thinking of you — always":                 false,
		"Thinking of you – always":                 false,
		"I miss you; come home":                    false,
		"See you at 8:":                            false,
		"":                                         false,
	} {
		if err := checkDraft(text); (err == nil) != ok {
			t.Errorf("checkDraft(%q) = %v, want ok %v", text, err, ok)
		}
	}
}

func TestTimesReadAsAMPM(t *testing.T) {
	for h, want := range map[int]string{0: "12 am", 9: "9 am", 12: "12 pm", 21: "9 pm"} {
		if got := hour(h); got != want {
			t.Errorf("hour(%d) = %q, want %q", h, got, want)
		}
	}
	st := state{User: store.User{Name: "Ada", Timezone: "Africa/Lagos", WakeHour: 9, SleepHour: 21, CheckInsPerDay: 2}}
	got := describe(st, time.Date(2026, 11, 4, 18, 5, 0, 0, time.UTC))
	for _, want := range []string{"7:05 pm", "between 9 am and 9 pm"} {
		if !strings.Contains(got, want) {
			t.Errorf("describe lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "19:05") || strings.Contains(got, "(Africa/Lagos)") {
		t.Errorf("describe shows a 24 hour time or the zone next to it:\n%s", got)
	}
}
