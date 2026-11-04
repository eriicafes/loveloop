package flows

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/eriicafes/go-workflow"
	"github.com/eriicafes/go-workflow/workflowai"
)

// chatTurn answers messages the user sent in a row as one turn.
func (f *Flows) chatTurn(ctx context.Context, chatID int64, texts []string) error {
	st, err := workflow.Step(ctx, f.loadState, chatID)
	if err != nil {
		return err
	}
	// A new message moves on from questions the user didn't answer.
	history := dropPendingApprovals(st.History)
	turn := make([]workflowai.ModelMessage, len(texts))
	for i, text := range texts {
		turn[i] = userMessage(text)
	}
	_, err = f.respond(ctx, st, history, turn, false, nil)
	return err
}

// approvalTurn carries out or cancels the pending tool call the user
// answered with a button, whose approval ID has the short key.
func (f *Flows) approvalTurn(ctx context.Context, chatID int64, key string, approved bool) error {
	st, err := workflow.Step(ctx, f.loadState, chatID)
	if err != nil {
		return err
	}
	id, ok := findPendingApproval(st.History, key)
	if !ok {
		_, err := workflow.Step(ctx, f.sendReply, reply{ChatID: chatID, Text: "That question has expired, just tell me what you'd like 💫"})
		return err
	}
	turn := []workflowai.ModelMessage{{
		Role:    workflowai.RoleTool,
		Content: []workflowai.ContentPart{{Type: workflowai.PartToolApprovalResponse, ApprovalID: id, Approved: approved}},
	}}
	_, err = f.respond(ctx, st, st.History, turn, true, nil)
	return err
}

// respond runs the agent on history followed by turn, saves the new
// messages, and sends the reply. activeTools limits the tools it may call.
//
// When turn answers approvals, the agent strips the approval parts from the
// conversation and drops turn, but history in the store still holds the
// request. respond saves turn as well, so resolveApprovals strips both parts
// when the history is loaded again.
func (f *Flows) respond(ctx context.Context, st state, history, turn []workflowai.ModelMessage, approval bool, activeTools []string) (string, error) {
	chatID := st.User.ChatID
	now, err := workflow.Now(ctx)
	if err != nil {
		return "", err
	}
	result, err := f.newAgent(st, now).Generate(ctx, workflowai.CallOptions{
		Messages:    append(slices.Clone(history), turn...),
		ActiveTools: activeTools,
	})
	if err != nil {
		_, _ = workflow.Step(ctx, f.sendReply, reply{ChatID: chatID, Text: "Oops, my heart skipped a beat 💓 Something went wrong, try again in a moment?"})
		return "", err
	}

	added := result.Messages[len(history):]
	if approval {
		added = append(slices.Clone(turn), added...)
	}
	if _, err := workflow.Step(ctx, f.appendHistory, historyAppend{ChatID: chatID, Messages: added}); err != nil {
		return "", err
	}
	r := reply{ChatID: chatID, Text: result.Text, Drafts: draftsIn(result), Approvals: approvalQuestions(result.Messages)}
	if r.Text == "" && len(r.Approvals) == 0 && len(result.ToolResults) == 0 {
		r.Text = "💞"
	}
	if _, err := workflow.Step(ctx, f.sendReply, r); err != nil {
		return "", err
	}
	return result.Text, f.compactHistory(ctx, chatID)
}

func userMessage(text string) workflowai.ModelMessage {
	return workflowai.ModelMessage{Role: workflowai.RoleUser, Content: []workflowai.ContentPart{{Type: workflowai.PartText, Text: text}}}
}

// approvalQuestions returns the questions to ask for the approval requests
// the last assistant message ended with.
func approvalQuestions(messages []workflowai.ModelMessage) []pendingApproval {
	if len(messages) == 0 || messages[len(messages)-1].Role != workflowai.RoleAssistant {
		return nil
	}
	content := messages[len(messages)-1].Content
	var approvals []pendingApproval
	for _, part := range content {
		if part.Type != workflowai.PartToolApprovalRequest {
			continue
		}
		question := "Should I go ahead?"
		i := slices.IndexFunc(content, func(c workflowai.ContentPart) bool {
			return c.Type == workflowai.PartToolCall && c.ToolCallID == part.ToolCallID
		})
		if i >= 0 && content[i].ToolName == "forgetPerson" {
			var in nameInput
			_ = json.Unmarshal(content[i].Input, &in)
			question = fmt.Sprintf("Forget %s and everything I know about them? 🥺", in.Name)
		}
		approvals = append(approvals, pendingApproval{ID: part.ApprovalID, Question: question})
	}
	return approvals
}

// resolveApprovals strips the approval requests that a later message
// answers, and those answers, as the agent does when it resumes approved
// calls. Only unanswered requests stay in the conversation.
func resolveApprovals(history []workflowai.ModelMessage) []workflowai.ModelMessage {
	answers := map[string]bool{}
	for _, message := range history {
		for _, part := range message.Content {
			if part.Type == workflowai.PartToolApprovalResponse {
				answers[part.ApprovalID] = true
			}
		}
	}
	if len(answers) == 0 {
		return history
	}
	resolved := make([]workflowai.ModelMessage, 0, len(history))
	for _, message := range history {
		message.Content = slices.DeleteFunc(slices.Clone(message.Content), func(c workflowai.ContentPart) bool {
			return (c.Type == workflowai.PartToolApprovalRequest || c.Type == workflowai.PartToolApprovalResponse) && answers[c.ApprovalID]
		})
		if len(message.Content) > 0 {
			resolved = append(resolved, message)
		}
	}
	return resolved
}

// findPendingApproval returns the ID of the unanswered approval request
// whose approvalKey is key.
func findPendingApproval(history []workflowai.ModelMessage, key string) (string, bool) {
	for _, id := range pendingApprovals(history) {
		if approvalKey(id) == key {
			return id, true
		}
	}
	return "", false
}

// pendingApprovals returns the approval requests in the last assistant
// message that no later message answers.
func pendingApprovals(history []workflowai.ModelMessage) []string {
	last := len(history) - 1
	for last >= 0 && history[last].Role != workflowai.RoleAssistant {
		last--
	}
	if last < 0 {
		return nil
	}
	var ids []string
	for _, part := range history[last].Content {
		if part.Type == workflowai.PartToolApprovalRequest && !answered(history[last+1:], part.ApprovalID) {
			ids = append(ids, part.ApprovalID)
		}
	}
	return ids
}

func answered(messages []workflowai.ModelMessage, approvalID string) bool {
	return slices.ContainsFunc(messages, func(m workflowai.ModelMessage) bool {
		return slices.ContainsFunc(m.Content, func(c workflowai.ContentPart) bool {
			return c.Type == workflowai.PartToolApprovalResponse && c.ApprovalID == approvalID
		})
	})
}

// dropPendingApprovals removes the tool calls that wait for approval from
// the conversation, so it stays valid when the user moves on without
// answering.
func dropPendingApprovals(history []workflowai.ModelMessage) []workflowai.ModelMessage {
	pending := pendingApprovals(history)
	if len(pending) == 0 {
		return history
	}
	var calls []string
	history = slices.Clone(history)
	for i, message := range history {
		if message.Role != workflowai.RoleAssistant {
			continue
		}
		content := slices.DeleteFunc(slices.Clone(message.Content), func(c workflowai.ContentPart) bool {
			if c.Type == workflowai.PartToolApprovalRequest && slices.Contains(pending, c.ApprovalID) {
				calls = append(calls, c.ToolCallID)
				return true
			}
			return false
		})
		history[i].Content = content
	}
	for i, message := range history {
		history[i].Content = slices.DeleteFunc(slices.Clone(message.Content), func(c workflowai.ContentPart) bool {
			return c.Type == workflowai.PartToolCall && slices.Contains(calls, c.ToolCallID)
		})
	}
	return slices.DeleteFunc(history, func(m workflowai.ModelMessage) bool { return len(m.Content) == 0 })
}
