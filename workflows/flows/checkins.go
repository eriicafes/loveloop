package flows

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/eriicafes/go-workflow"

	"github.com/eriicafes/loveloop/lib/store"
	"github.com/eriicafes/loveloop/workflows"
)

// CheckIns texts the user first at a few random times over the day after
// input.From, within their waking hours, then starts the run for the next
// day. Each user has one chain of these runs instead of a cron job, and
// starting a new run every day keeps each run's event log short. The run
// sleeps between check-ins, so it holds no resources while it waits.
func (f *Flows) CheckIns(ctx context.Context, input workflows.CheckIns) (workflows.CheckInsResult, error) {
	var result workflows.CheckInsResult
	user, err := workflow.Step(ctx, f.loadUser, input.ChatID)
	if err != nil {
		return result, err
	}
	// A paused user's chain keeps going, so resuming needs no restart.
	var times []time.Time
	if !user.Paused && user.CheckInsPerDay > 0 {
		times, err = workflow.InlineStep(ctx, pickTimes, timesInput{User: user, From: input.From})
		if err != nil {
			return result, err
		}
	}
	for _, at := range times {
		if err := sleepUntil(ctx, at); err != nil {
			return result, err
		}
		reason, err := workflow.Step(ctx, f.planCheckIn, input.ChatID)
		if err != nil {
			return result, err
		}
		if reason == "" {
			continue
		}
		mail := scoped[store.Mail]{ChatID: input.ChatID, Input: store.Mail{Nudge: reason}}
		if _, err := workflow.Step(ctx, f.deliverMail, mail); err != nil {
			return result, err
		}
		result.Sent++
	}

	next := input.From.Add(24 * time.Hour)
	if err := sleepUntil(ctx, next); err != nil {
		return result, err
	}
	run, err := workflow.Start(ctx, workflows.CheckIns{ChatID: input.ChatID, From: next})
	if err != nil {
		return result, err
	}
	_, err = workflow.Step(ctx, f.saveCheckInsRun, scoped[string]{ChatID: input.ChatID, Input: run.ID()})
	return result, err
}

// saveCheckInsRun records the run that now carries the user's check-ins.
func (f *Flows) saveCheckInsRun(ctx context.Context, input scoped[string]) (struct{}, error) {
	return struct{}{}, f.Store.SetCheckInsRun(ctx, input.ChatID, input.Input)
}

func (f *Flows) loadUser(ctx context.Context, chatID int64) (store.User, error) {
	return f.Store.User(ctx, chatID)
}

type timesInput struct {
	User store.User `json:"user"`
	From time.Time  `json:"from"`
}

// pickTimes picks the user's check-in times at random among the quarter
// hours of the day after From that fall in their waking hours, at least an
// hour and a half apart when it can.
func pickTimes(_ context.Context, input timesInput) ([]time.Time, error) {
	loc := input.User.Location()
	var slots []time.Time
	for t := input.From.Truncate(15 * time.Minute).Add(15 * time.Minute); t.Before(input.From.Add(24 * time.Hour)); t = t.Add(15 * time.Minute) {
		if awake(t.In(loc).Hour(), input.User.WakeHour, input.User.SleepHour) {
			slots = append(slots, t)
		}
	}
	rand.Shuffle(len(slots), func(i, j int) { slots[i], slots[j] = slots[j], slots[i] })
	var picked []time.Time
	for _, gap := range []time.Duration{90 * time.Minute, 0} {
		for _, slot := range slots {
			if len(picked) == input.User.CheckInsPerDay {
				break
			}
			if !slices.ContainsFunc(picked, func(p time.Time) bool { d := p.Sub(slot); return d.Abs() < gap || d == 0 }) {
				picked = append(picked, slot)
			}
		}
	}
	slices.SortFunc(picked, time.Time.Compare)
	return picked, nil
}

// awake reports whether hour is in the waking hours from wake until sleep,
// which may wrap past midnight.
func awake(hour, wake, sleep int) bool {
	if wake <= sleep {
		return hour >= wake && hour < sleep
	}
	return hour >= wake || hour < sleep
}

// planCheckIn picks what the next check-in is about, preferring loved ones
// the user is overdue to contact and dates coming up. It returns "" to skip
// the check-in, when the user paused check-ins or is chatting already.
func (f *Flows) planCheckIn(ctx context.Context, chatID int64) (string, error) {
	user, err := f.Store.User(ctx, chatID)
	if err != nil {
		return "", err
	}
	if user.Paused || time.Since(user.LastMessageAt) < 45*time.Minute {
		return "", nil
	}
	people, err := f.Store.People(ctx, chatID)
	if err != nil {
		return "", err
	}
	if len(people) == 0 {
		// Loved ones come up in conversation, so don't ask about them.
		return pick(
			"Send a short, warm hello to brighten their day.",
			"Share a tiny, fun relationship tip or love fact.",
		), nil
	}

	now := time.Now().In(user.Location())
	var ideas []string
	for _, p := range people {
		if p.KeepInTouchDays > 0 && !p.LastContactAt.IsZero() && now.Sub(p.LastContactAt) >= time.Duration(p.KeepInTouchDays)*24*time.Hour {
			ideas = append(ideas, fmt.Sprintf("It's been a while since they were in touch with %s. Nudge them gently and offer a cute text to send with draftText.", p.Name))
		}
		for _, date := range []struct{ label, value string }{{"birthday", p.Birthday}, {"anniversary", p.Anniversary}} {
			if days, ok := daysUntil(date.value, now); ok && days <= 14 {
				ideas = append(ideas, fmt.Sprintf("%s's %s is in %d days. Help them get ready: a plan, a gift idea, or a sweet message.", p.Name, date.label, days))
			}
		}
	}
	if len(ideas) > 0 && rand.N(4) > 0 {
		return pick(ideas...), nil
	}
	p := people[rand.N(len(people))]
	return pick(
		fmt.Sprintf("Suggest a cute, specific text they could send %s right now, using what you know about them, with draftText.", p.Name),
		fmt.Sprintf("Ask how things are going with %s, in a fun way.", p.Name),
		fmt.Sprintf("Suggest a small, thoughtful gesture for %s based on their likes or love language.", p.Name),
		fmt.Sprintf("Ask a playful question that helps you learn something new about %s.", p.Name),
		fmt.Sprintf("Suggest a fun date or hangout idea with %s that suits them.", p.Name),
		"Send a quick, upbeat check-in about their relationships, with a light joke or a tiny love fact.",
	), nil
}

func pick(options ...string) string { return options[rand.N(len(options))] }
