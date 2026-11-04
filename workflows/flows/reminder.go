package flows

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/eriicafes/go-workflow"

	"github.com/eriicafes/loveloop/lib/store"
	"github.com/eriicafes/loveloop/workflows"
)

// maxSleep keeps each sleep within the seven days Vercel Queues can delay a
// delivery, so long reminders work without QStash.
const maxSleep = 6 * 24 * time.Hour

// Reminder is one link of a reminder's chain of runs. It sleeps toward
// input.At for at most maxSleep, and once the reminder is due has the agent
// remind the user. Then, unless the reminder was one-off and has fired, it
// starts the next run, for the same time or the next repeat, and ends. Each
// run lasts days at most, and Reminder embeds workflow.StartOnLatest, so the
// chain moves to new deployments instead of outliving its first one. The
// reminder's ID in the store is the first run's ID, and the store keeps the
// current run's ID so cancelling the reminder cancels that run.
func (f *Flows) Reminder(ctx context.Context, input workflows.Reminder) (workflows.ReminderResult, error) {
	var result workflows.ReminderResult
	if input.ID == "" {
		input.ID = workflow.GetWorkflowMetadata(ctx).RunID
	}
	now, err := workflow.Now(ctx)
	if err != nil {
		return result, err
	}
	if wait := input.At.Sub(now); wait > 0 {
		if err := workflow.Sleep(ctx, min(wait, maxSleep)); err != nil {
			return result, err
		}
		if now, err = workflow.Now(ctx); err != nil {
			return result, err
		}
	}
	active, err := workflow.Step(ctx, f.reminderActive, scoped[string]{ChatID: input.ChatID, Input: input.ID})
	if err != nil || !active {
		return result, err
	}
	if input.At.Sub(now) > 0 {
		return result, f.continueReminder(ctx, input)
	}

	reason := fmt.Sprintf("Reminder the user asked for: %q.", input.About)
	if input.Person != "" {
		reason += fmt.Sprintf(" It's about %s; write a message for them with draftText if it fits.", input.Person)
	}
	mail := scoped[store.Mail]{ChatID: input.ChatID, Input: store.Mail{Nudge: reason}}
	if _, err := workflow.Step(ctx, f.deliverMail, mail); err != nil {
		return result, err
	}
	if input.Person != "" {
		followUp := store.Person{Name: input.Person, FollowUp: fmt.Sprintf("you reminded the user to %s", input.About)}
		if _, err := workflow.Step(ctx, f.setFollowUp, scoped[store.Person]{ChatID: input.ChatID, Input: followUp}); err != nil {
			return result, err
		}
	}
	result.Fired++
	if input.EveryDays <= 0 {
		_, err := workflow.Step(ctx, f.deleteReminder, scoped[string]{ChatID: input.ChatID, Input: input.ID})
		return result, err
	}
	input.At = input.At.AddDate(0, 0, input.EveryDays)
	return result, f.continueReminder(ctx, input)
}

// continueReminder starts the reminder's next run and records it as the
// reminder's current run.
func (f *Flows) continueReminder(ctx context.Context, next workflows.Reminder) error {
	run, err := workflow.Start(ctx, next)
	if err != nil {
		return err
	}
	moved := store.Reminder{ID: next.ID, RunID: run.ID(), At: next.At}
	_, err = workflow.Step(ctx, f.moveReminder, scoped[store.Reminder]{ChatID: next.ChatID, Input: moved})
	return err
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

// moveReminder records a reminder's current run and next time, unless the
// reminder was cancelled meanwhile.
func (f *Flows) moveReminder(ctx context.Context, input scoped[store.Reminder]) (struct{}, error) {
	current, err := f.Store.Reminder(ctx, input.ChatID, input.Input.ID)
	if errors.Is(err, store.ErrNotFound) {
		return struct{}{}, nil
	} else if err != nil {
		return struct{}{}, err
	}
	current.RunID, current.At = input.Input.RunID, input.Input.At
	return struct{}{}, f.Store.SaveReminder(ctx, input.ChatID, current)
}

// deleteReminder deletes a one-off reminder that has fired.
func (f *Flows) deleteReminder(ctx context.Context, input scoped[string]) (struct{}, error) {
	return struct{}{}, f.Store.DeleteReminder(ctx, input.ChatID, input.Input)
}

// setFollowUp sets a saved person's follow-up, so the agent asks how it went
// next time. It does nothing for someone LoveLoop doesn't know.
func (f *Flows) setFollowUp(ctx context.Context, input scoped[store.Person]) (struct{}, error) {
	person, err := f.Store.Person(ctx, input.ChatID, input.Input.Name)
	if errors.Is(err, store.ErrNotFound) {
		return struct{}{}, nil
	} else if err != nil {
		return struct{}{}, err
	}
	person.FollowUp, person.FollowUpAt = input.Input.FollowUp, time.Now()
	return struct{}{}, f.Store.SavePerson(ctx, input.ChatID, person)
}
