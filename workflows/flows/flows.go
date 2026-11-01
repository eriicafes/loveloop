// Package flows holds LoveLoop's workflow runners and the steps they call,
// as methods on Flows, which carries their dependencies.
package flows

import (
	"os"

	"github.com/digitallysavvy/go-ai/pkg/provider"
	"github.com/eriicafes/go-workflow"
	"github.com/eriicafes/go-workflow/worlds"
	"github.com/eriicafes/got"

	"github.com/eriicafes/loveloop/store"
	"github.com/eriicafes/loveloop/telegram"
)

// Flows runs LoveLoop's workflows. Its methods are the runners and the
// steps they call, so steps reach the dependencies through the receiver
// rather than their recorded input.
type Flows struct {
	Store *store.Store
	Bot   *telegram.Client
	Model provider.LanguageModel
	// ToolApprovalSecret signs tool approval requests, or is nil to leave
	// them unsigned.
	ToolApprovalSecret []byte
}

// GetFlows returns Flows configured from the environment.
var GetFlows = got.TryUsing(func(c *got.Container) (*Flows, error) {
	s, err := store.GetStore.From(c)
	if err != nil {
		return nil, err
	}
	model, err := GetModel.From(c)
	if err != nil {
		return nil, err
	}
	f := &Flows{Store: s, Bot: telegram.GetClient.From(c), Model: model}
	if secret := os.Getenv("TOOL_APPROVAL_SECRET"); secret != "" {
		f.ToolApprovalSecret = []byte(secret)
	}
	return f, nil
})

// GetRunners returns the runners of LoveLoop's workflows, building Flows
// first.
var GetRunners = got.TryUsing(func(c *got.Container) (worlds.RunnerSet, error) {
	f, err := GetFlows.From(c)
	if err != nil {
		return nil, err
	}
	return worlds.Runners(
		workflow.Use(f.Chat),
		workflow.Use(f.CheckIns),
		workflow.Use(f.Reminder),
	), nil
})
