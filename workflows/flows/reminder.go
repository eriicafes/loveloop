package flows

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/eriicafes/go-workflow"

	"github.com/eriicafes/loveloop/store"
	"github.com/eriicafes/loveloop/workflows"
)

// maxSleep keeps each sleep within the seven days Vercel Queues can delay a
// delivery, so long reminders work without QStash.
const maxSleep = 6 * 24 * time.Hour

// Reminder sleeps until input.At and has the agent remind the user, then
// does it again every input.EveryDays days until the reminder is cancelled.
// The reminder's ID in the store is this run's ID.
func (f *Flows) Reminder(ctx context.Context, input workflows.Reminder) (workflows.ReminderResult, error) {
	id := workflow.GetWorkflowMetadata(ctx).RunID
	var result workflows.ReminderResult
	for at := input.At; ; at = at.AddDate(0, 0, input.EveryDays) {
		if err := sleepUntil(ctx, at); err != nil {
			return result, err
		}
		active, err := workflow.Step(ctx, f.reminderActive, scoped[string]{ChatID: input.ChatID, Input: id})
		if err != nil || !active {
			return result, err
		}
		reason := fmt.Sprintf("Reminder the user asked for: %q.", input.About)
		if input.Person != "" {
			reason += fmt.Sprintf(" It's about %s; offer a cute text for them with draftText if it fits.", input.Person)
		}
		mail := scoped[store.Mail]{ChatID: input.ChatID, Input: store.Mail{Nudge: reason}}
		if _, err := workflow.Step(ctx, f.deliverMail, mail); err != nil {
			return result, err
		}
		result.Fired++

		next := store.Reminder{ID: id, At: at.AddDate(0, 0, input.EveryDays), About: input.About, Person: input.Person, EveryDays: input.EveryDays}
		if _, err := workflow.Step(ctx, f.advanceReminder, scoped[store.Reminder]{ChatID: input.ChatID, Input: next}); err != nil {
			return result, err
		}
		if input.EveryDays <= 0 {
			return result, nil
		}
	}
}

// sleepUntil sleeps until at, in sleeps of at most maxSleep.
func sleepUntil(ctx context.Context, at time.Time) error {
	for {
		now, err := workflow.Now(ctx)
		if err != nil {
			return err
		}
		wait := at.Sub(now)
		if wait <= 0 {
			return nil
		}
		if err := workflow.Sleep(ctx, min(wait, maxSleep)); err != nil {
			return err
		}
	}
}

// reminderActive reports whether the reminder is still saved, which it is
// not once the user cancels it.
func (f *Flows) reminderActive(ctx context.Context, input scoped[string]) (bool, error) {
	_, err := f.Store.Reminder(ctx, input.ChatID, input.Input)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// advanceReminder moves a repeating reminder to its next time, and deletes
// a one-off reminder that has fired.
func (f *Flows) advanceReminder(ctx context.Context, input scoped[store.Reminder]) (struct{}, error) {
	current, err := f.Store.Reminder(ctx, input.ChatID, input.Input.ID)
	if errors.Is(err, store.ErrNotFound) {
		return struct{}{}, nil
	} else if err != nil {
		return struct{}{}, err
	}
	if input.Input.EveryDays <= 0 {
		return struct{}{}, f.Store.DeleteReminder(ctx, input.ChatID, input.Input.ID)
	}
	current.At = input.Input.At
	return struct{}{}, f.Store.SaveReminder(ctx, input.ChatID, current)
}
