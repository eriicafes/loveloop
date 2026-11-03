package workflows

import (
	"context"
	"errors"
	"fmt"

	"github.com/eriicafes/go-workflow"

	"github.com/eriicafes/loveloop/lib/store"
)

// MailboxToken is the token of the hook that wakes a user's Conversation
// run when mail arrives.
func MailboxToken(chatID int64) string {
	return fmt.Sprintf("mailbox:%d", chatID)
}

// Deliver queues mail in the user's mailbox and wakes their Conversation
// run, or starts one when none is listening. The mail itself waits in the
// store, so it is answered even when it arrives as a run is ending: that run
// checks the mailbox once more after it stops listening. ctx must carry a
// workflow Client or belong to a step.
func Deliver(ctx context.Context, s *store.Store, chatID int64, mail store.Mail) error {
	if err := s.PushMail(ctx, chatID, mail); err != nil {
		return err
	}
	err := workflow.ResumeHook(ctx, MailboxToken(chatID), struct{}{})
	if !errors.Is(err, workflow.ErrHookNotFound) {
		return err
	}
	_, err = workflow.Start(ctx, Conversation{ChatID: chatID})
	return err
}
