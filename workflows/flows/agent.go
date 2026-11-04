package flows

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/digitallysavvy/go-ai/pkg/ai"
	"github.com/eriicafes/go-workflow"
	"github.com/eriicafes/go-workflow/workflowai"

	"github.com/eriicafes/loveloop/lib/store"
	"github.com/eriicafes/loveloop/workflows"
)

const persona = `You are LoveLoop, the user's sidekick for their relationships, living in Telegram. Think of
yourself as their partner in crime: you're fully on their side and you have their back, and
you know them well enough to call them out, playfully, when you spot a pattern. Juggling a few
too many crushes, going quiet on someone, dodging a question, or not quite telling the whole
truth all earn a cheeky remark. You care about the user and about the people in their life too,
so show empathy for both.

How you talk:
- Short, casual Telegram messages, the way a close friend texts. Plain text only, no Markdown.
  Emojis are welcome but don't overdo it.
- No formal greetings or sign-offs, and don't keep using the user's name.
- Be someone they can talk to, not just a to-do list. When they share something, react to it and
  listen before jumping to tasks or suggestions.
- Never offer to do things. No "want me to...", "should I...", "I can also...", "let me know
  if...", "need help with...", or any other offer or call to action, at the end of a message or
  anywhere in it. The user tells you what they want, and they know they can reach you any time.
  End on a reaction or a remark, not a question. Ask a question only out of real curiosity about
  their life, and only now and then. The one exception: point out something time sensitive, like
  a birthday tomorrow, but say it as a heads up, not an offer.
- Fun and teasing, never preachy or guilt-trippy. Celebrate the small stuff.
- Talk about "the people in your life", "your people", or who someone is to the user. Never say
  "loved ones" or other terms from these instructions to the user.
- Times below are in the user's time zone. Say them like "7:30 pm" and never mention the time zone.

What you do:
- Remember everyone the user mentions. Whenever you learn a name, relationship, nickname, birthday,
  anniversary, likes, dislikes or love language, save it right away with rememberPerson. Also save
  facts that help you understand the person: what they're like, what's going on with them, how
  they treat the user, how the user feels about them. Don't ask permission to remember.
- Learn about people as the user brings them up. Don't quiz the user about who they love.
- Keep an eye on how things change over time. Use what you've saved and your earlier conversations
  to notice trends, and bring them up when it's fun or when it helps.
- When the user says they talked to, saw or texted someone, call logContact.
- When the user wants to be reminded, call scheduleReminder. Work out the exact local date and time
  from the current time below; pick a sensible time if they only give a day. Use everyDays for
  repeating nudges such as "remind me to call mom every Sunday".
- When the user wants a message for someone, or a check-in would be nicer with one, write it in
  the user's voice and give it with draftText, never in your reply. You never message anyone
  yourself. In the message:
  - Match how the user feels about the person, using what you know about them.
  - Write the way people really text. No em dashes, colons or semicolons.
  - Prefer a sweet pet name over their name, especially for a partner or crush, so it shows how
    close they are.
  draftText is always your last call, and your turn ends with it. Anything you want to say goes in
  the same response, before the draft, and stays short: a quick reaction at most, or nothing. The
  user then gets the draft as its own message and a reply tagging it with who it's for. Never tell
  them to copy or send it, and never offer to change it, its vibe, when to send it, or anything
  to do next.
- Someone with "waiting to hear" below has something the user was going to do, like send a message
  you drafted. When the user next talks to you, in a later conversation, casually ask how it went
  or whether they sent it, once. When you find out, or they'd rather not say, call closeFollowUp.
- Use forgetPerson only when the user clearly asks you to forget someone.
- Use updateSettings when the user tells you their name or what to call them, where they are,
  waking hours, how often they want you to check in, or asks you to pause or resume check-ins.
- Messages that start with "[LoveLoop scheduler" are not from the user: they are your own scheduled
  check-ins and reminders. Write the message to send the user right now, as if you just thought of them.

You're only here for the user's relationships. If they ask about themselves, answer in terms of
their relationships and the people in their life. Turn down anything else, like homework, code,
trivia or general questions, politely and with a cheeky line, and steer back to their people.

When you first meet the user, introduce yourself briefly and ask what they'd like to be called.
Once you know their name, if their time zone isn't set, ask where they're based. Only if their
answer doesn't narrow it down to a city you recognize, ask for their city or the closest big one.
Then save its time zone with updateSettings.`

// newAgent builds the agent for one turn with st as what it knows. now is
// the run's current time.
func (f *Flows) newAgent(st state, now time.Time) *workflowai.WorkflowAgent {
	chatID := st.User.ChatID
	u := userTools{Flows: f, ChatID: chatID}
	return workflowai.NewWorkflowAgent(workflowai.WorkflowAgentOptions{
		ID:           "loveloop",
		Model:        f.Model,
		Instructions: persona + "\n\n" + describe(st, now),
		Tools: workflowai.ToolSet{
			"rememberPerson": {
				Description: "Create or update someone in the user's life. Only the fields you set change; list fields are added to.",
				InputSchema: schemaFor[personInput](),
				Execute:     workflowai.ExecuteStep(u.rememberPerson),
			},
			"forgetPerson": {
				Description:   "Forget someone and everything about them. Asks the user to confirm.",
				NeedsApproval: workflowai.NeedsApprovalAlways,
				InputSchema:   schemaFor[nameInput](),
				Execute:       workflowai.ExecuteStep(u.forgetPerson),
			},
			"logContact": {
				Description: "Record that the user was just in touch with someone. Also closes their follow-up.",
				InputSchema: schemaFor[contactInput](),
				Execute:     workflowai.ExecuteStep(u.logContact),
			},
			"draftText": {
				Description: "Give the user a message to copy and send to someone in their life. Call it last: your turn ends with it.",
				InputSchema: schemaFor[draft](),
				Execute:     workflowai.ExecuteStep(u.queueDraft),
			},
			"closeFollowUp": {
				Description: "Close someone's follow-up once you know how it went, or the user would rather not say.",
				InputSchema: schemaFor[followUpOutcome](),
				Execute:     workflowai.ExecuteStep(u.closeFollowUp),
			},
			"scheduleReminder": {
				Description: fmt.Sprintf("Remind the user about something at a local date and time, once or repeating. "+
					"Reminders can be at most a year ahead, repeat at most daily and at least yearly, and a user can have "+
					"at most %d; if they hit that, tell them which ones they could cancel.", maxActiveReminders),
				InputSchema: schemaFor[reminderInput](),
				Execute: workflowai.ExecuteFunc(func(ctx context.Context, raw json.RawMessage, _ workflowai.ToolCallOptions) (any, error) {
					return u.scheduleReminder(ctx, st.User.Location(), raw)
				}),
			},
			"cancelReminder": {
				Description: "Cancel one of the user's reminders by its id.",
				InputSchema: schemaFor[reminderID](),
				Execute:     workflowai.ExecuteStep(u.cancelReminder),
			},
			"updateSettings": {
				Description: "Change the user's name, time zone, waking hours, daily check-ins, or pause them.",
				InputSchema: schemaFor[settingsInput](),
				Execute:     workflowai.ExecuteStep(u.updateSettings),
			},
		},
		StopWhen:           []workflowai.StopCondition{workflowai.IsStepCount(8), draftQueued},
		ToolApprovalSecret: f.ToolApprovalSecret,
	})
}

// draftQueued ends the agent's turn once it has queued a draft, so nothing
// follows the draft and its "For" reply. A rejected draft doesn't count, so
// the agent can rewrite it.
func draftQueued(steps []workflowai.StepResult) bool {
	if len(steps) == 0 {
		return false
	}
	return slices.ContainsFunc(steps[len(steps)-1].ToolResults, func(result workflowai.ToolResult) bool {
		return result.ToolName == "draftText" && !result.IsError
	})
}

// describe tells the agent what it knows about the user, so it does not
// need a tool call to look things up.
func describe(st state, now time.Time) string {
	loc := st.User.Location()
	local := now.In(loc)
	var b strings.Builder
	fmt.Fprintf(&b, "Current time for the user: %s.\n", local.Format("Monday, 2 January 2006 "+clock))
	fmt.Fprintf(&b, "User: %s. Time zone: %s. Check-ins: %d a day between %s and %s", st.User.Name, cmp.Or(st.User.Timezone, "not set, times are UTC"), st.User.CheckInsPerDay, hour(st.User.WakeHour), hour(st.User.SleepHour))
	if st.User.Paused {
		b.WriteString(", paused")
	}
	b.WriteString(".\n\nThe people in the user's life:\n")
	if len(st.People) == 0 {
		b.WriteString("None yet. You'll learn about them as the user mentions them.\n")
	}
	for _, p := range st.People {
		saved := p
		saved.FollowUp, saved.FollowUpAt = "", time.Time{}
		data, _ := json.Marshal(saved)
		fmt.Fprintf(&b, "- %s", data)
		if !p.LastContactAt.IsZero() {
			days := int(now.Sub(p.LastContactAt).Hours() / 24)
			fmt.Fprintf(&b, " (last in touch %d days ago", days)
			if p.KeepInTouchDays > 0 && days >= p.KeepInTouchDays {
				b.WriteString(", OVERDUE")
			}
			b.WriteString(")")
		}
		for _, date := range []struct{ label, value string }{{"birthday", p.Birthday}, {"anniversary", p.Anniversary}} {
			if days, ok := daysUntil(date.value, local); ok && days <= 21 {
				fmt.Fprintf(&b, " (%s in %d days)", date.label, days)
			}
		}
		if p.FollowUp != "" {
			fmt.Fprintf(&b, " (waiting to hear: %s, since %s)", p.FollowUp, p.FollowUpAt.In(loc).Format("Mon 2 Jan "+clock))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nReminders:\n")
	if len(st.Reminders) == 0 {
		b.WriteString("None.\n")
	}
	for _, r := range st.Reminders {
		fmt.Fprintf(&b, "- id %s: %q at %s", r.ID, r.About, r.At.In(loc).Format("Mon 2 Jan 2006 "+clock))
		if r.EveryDays > 0 {
			fmt.Fprintf(&b, ", every %d days", r.EveryDays)
		}
		b.WriteString("\n")
	}
	if st.Summary != "" {
		b.WriteString("\nEarlier in your conversation, before the messages below:\n")
		b.WriteString(st.Summary + "\n")
	}
	return b.String()
}

// daysUntil returns the days from now until the next yearly occurrence of
// date, written YYYY-MM-DD or MM-DD.
func daysUntil(date string, now time.Time) (int, bool) {
	if len(date) == len("2006-01-02") {
		date = date[5:]
	}
	parsed, err := time.Parse("01-02", date)
	if err != nil {
		return 0, false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	next := time.Date(now.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
	if next.Before(today) {
		next = next.AddDate(1, 0, 0)
	}
	return int(next.Sub(today).Hours() / 24), true
}

// clock is the layout of a time of day as the user reads it, such as
// "7:30 pm".
const clock = "3:04 pm"

// hour describes an hour of the day, such as "9 am".
func hour(h int) string {
	return time.Date(2000, 1, 1, h, 0, 0, 0, time.UTC).Format("3 pm")
}

// scoped is the input of a step that acts for one user.
type scoped[In any] struct {
	ChatID int64 `json:"chatId"`
	Input  In    `json:"input"`
}

// userTools holds the agent's tool steps for one user. They reach Flows'
// dependencies and the user's chat ID through the receiver, so the model's
// tool input is all they take, and the model can't pick another user.
type userTools struct {
	*Flows
	ChatID int64
}

// schemaFor returns the JSON schema of T's JSON fields, with each field's
// desc tag as its description.
func schemaFor[T any]() map[string]any {
	schema := ai.SchemaFor[T]().Validator().JSONSchema()
	properties, _ := schema["properties"].(map[string]any)
	t := reflect.TypeFor[T]()
	for i := range t.NumField() {
		field := t.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if property, ok := properties[name].(map[string]any); ok && field.Tag.Get("desc") != "" {
			property["description"] = field.Tag.Get("desc")
		}
	}
	return schema
}

type personInput struct {
	Name            string   `json:"name" desc:"The person's name, as the user calls them."`
	Rename          string   `json:"rename,omitempty" desc:"A new name, to correct or change the saved name."`
	Relationship    string   `json:"relationship,omitempty" desc:"For example girlfriend, husband, mom, best friend."`
	Nickname        string   `json:"nickname,omitempty" desc:"Pet name or nickname the user uses for them."`
	Birthday        string   `json:"birthday,omitempty" desc:"YYYY-MM-DD, or MM-DD when the year is unknown."`
	Anniversary     string   `json:"anniversary,omitempty" desc:"YYYY-MM-DD, or MM-DD when the year is unknown."`
	LoveLanguage    string   `json:"loveLanguage,omitempty" desc:"How they like to feel loved."`
	KeepInTouchDays int      `json:"keepInTouchDays,omitempty" desc:"Nudge the user when they haven't been in touch for this many days."`
	AddLikes        []string `json:"addLikes,omitempty" desc:"Things they love."`
	AddDislikes     []string `json:"addDislikes,omitempty" desc:"Things they dislike."`
	AddFacts        []string `json:"addFacts,omitempty" desc:"Facts that help understand them: what they're like, what's going on with them, how they treat the user, how the user feels about them, plans, inside jokes."`
	Forget          []string `json:"forget,omitempty" desc:"Likes, dislikes or facts that are no longer true, as saved."`
}

func (u userTools) rememberPerson(ctx context.Context, in personInput) (store.Person, error) {
	if strings.TrimSpace(in.Name) == "" {
		return store.Person{}, workflow.FatalError(errors.New("name is required"))
	}
	person, err := u.Store.Person(ctx, u.ChatID, in.Name)
	if errors.Is(err, store.ErrNotFound) {
		person = store.Person{Name: strings.TrimSpace(in.Name)}
	} else if err != nil {
		return store.Person{}, err
	}
	if in.Rename != "" && store.PersonKey(in.Rename) != person.Key() {
		if err := u.Store.DeletePerson(ctx, u.ChatID, person.Name); err != nil && !errors.Is(err, store.ErrNotFound) {
			return store.Person{}, err
		}
		person.Name = strings.TrimSpace(in.Rename)
	}
	set := func(field *string, value string) {
		if value != "" {
			*field = value
		}
	}
	set(&person.Relationship, in.Relationship)
	set(&person.Nickname, in.Nickname)
	set(&person.Birthday, in.Birthday)
	set(&person.Anniversary, in.Anniversary)
	set(&person.LoveLanguage, in.LoveLanguage)
	if in.KeepInTouchDays > 0 {
		person.KeepInTouchDays = in.KeepInTouchDays
	}
	person.Likes = merge(person.Likes, in.AddLikes, in.Forget)
	person.Dislikes = merge(person.Dislikes, in.AddDislikes, in.Forget)
	person.Facts = merge(person.Facts, in.AddFacts, in.Forget)
	person.UpdatedAt = time.Now()
	return person, u.Store.SavePerson(ctx, u.ChatID, person)
}

// merge adds to list the values it lacks and removes the forgotten ones,
// ignoring case.
func merge(list, add, forget []string) []string {
	has := func(values []string, v string) bool {
		return slices.ContainsFunc(values, func(x string) bool { return strings.EqualFold(x, v) })
	}
	list = slices.DeleteFunc(slices.Clone(list), func(v string) bool { return has(forget, v) })
	for _, v := range add {
		if v = strings.TrimSpace(v); v != "" && !has(list, v) {
			list = append(list, v)
		}
	}
	return list
}

type nameInput struct {
	Name string `json:"name" desc:"The person's name, as saved."`
}

func (u userTools) forgetPerson(ctx context.Context, in nameInput) (string, error) {
	err := u.Store.DeletePerson(ctx, u.ChatID, in.Name)
	if errors.Is(err, store.ErrNotFound) {
		return "", workflow.FatalError(err)
	}
	return "Forgotten.", err
}

type contactInput struct {
	Name string `json:"name" desc:"The person's name, as saved."`
	Note string `json:"note,omitempty" desc:"Anything worth remembering from the conversation, saved as a fact."`
}

func (u userTools) logContact(ctx context.Context, in contactInput) (store.Person, error) {
	person, err := u.Store.Person(ctx, u.ChatID, in.Name)
	if errors.Is(err, store.ErrNotFound) {
		return store.Person{}, workflow.FatalError(fmt.Errorf("%w; save them with rememberPerson first", err))
	} else if err != nil {
		return store.Person{}, err
	}
	person.LastContactAt = time.Now()
	person.UpdatedAt = person.LastContactAt
	person.Facts = merge(person.Facts, []string{in.Note}, nil)
	person.FollowUp, person.FollowUpAt = "", time.Time{}
	return person, u.Store.SavePerson(ctx, u.ChatID, person)
}

type followUpOutcome struct {
	Name    string `json:"name" desc:"The person's name, as saved."`
	Outcome string `json:"outcome,omitempty" desc:"How it went, saved as a fact. Omit if the user would rather not say."`
}

// closeFollowUp clears a person's follow-up, saving how it went.
func (u userTools) closeFollowUp(ctx context.Context, in followUpOutcome) (store.Person, error) {
	person, err := u.Store.Person(ctx, u.ChatID, in.Name)
	if errors.Is(err, store.ErrNotFound) {
		return store.Person{}, workflow.FatalError(err)
	} else if err != nil {
		return store.Person{}, err
	}
	person.Facts = merge(person.Facts, []string{in.Outcome}, nil)
	person.FollowUp, person.FollowUpAt = "", time.Time{}
	person.UpdatedAt = time.Now()
	return person, u.Store.SavePerson(ctx, u.ChatID, person)
}

type reminderInput struct {
	At        string `json:"at" desc:"Local date and time in the user's time zone, YYYY-MM-DDTHH:MM."`
	About     string `json:"about" desc:"What to remind the user about, in a few words."`
	Person    string `json:"person,omitempty" desc:"The person it is about, if any, as saved."`
	EveryDays int    `json:"everyDays,omitempty" desc:"Repeat every this many days, 1 to 365, for example 7 for weekly. Omit for once."`
}

type scheduledReminder struct {
	ID        string `json:"id"`
	At        string `json:"at"`
	EveryDays int    `json:"everyDays,omitempty"`
}

func (u userTools) reminderCount(ctx context.Context, _ struct{}) (int64, error) {
	return u.Store.ReminderCount(ctx, u.ChatID)
}

// Reminder limits keep the work they schedule small. A Reminder run waits in
// sleeps of at most maxSleep, under the seven days Vercel Queues and the
// QStash free plan can delay, so no reminder needs QStash, and a year-ahead
// reminder wakes about sixty times on the way. Daily repeats and a cap per
// user bound how often reminders fire.
const (
	maxReminderAhead   = 366 * 24 * time.Hour
	maxReminderEvery   = 365
	maxActiveReminders = 20
)

// scheduleReminder starts a Reminder run, which sleeps until the reminder is
// due, and saves it under the run's ID. It runs in workflow context, with the
// time read in the user's time zone loc.
func (u userTools) scheduleReminder(ctx context.Context, loc *time.Location, raw json.RawMessage) (scheduledReminder, error) {
	var in reminderInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return scheduledReminder{}, err
	}
	at, err := time.ParseInLocation("2006-01-02T15:04", in.At, loc)
	if err != nil {
		return scheduledReminder{}, fmt.Errorf("at must be YYYY-MM-DDTHH:MM: %w", err)
	}
	now, err := workflow.Now(ctx)
	if err != nil {
		return scheduledReminder{}, err
	}
	if !at.After(now) {
		return scheduledReminder{}, fmt.Errorf("%s is in the past; it is now %s", in.At, now.In(loc).Format("2006-01-02T15:04"))
	}
	if at.Sub(now) > maxReminderAhead {
		return scheduledReminder{}, errors.New("reminders can be at most a year ahead; suggest a nearer date, or a yearly repeat")
	}
	if in.EveryDays < 0 || in.EveryDays > maxReminderEvery {
		return scheduledReminder{}, fmt.Errorf("everyDays must be between 1 and %d, or 0 for once", maxReminderEvery)
	}
	count, err := workflow.Step(ctx, u.reminderCount, struct{}{})
	if err != nil {
		return scheduledReminder{}, err
	}
	if count >= maxActiveReminders {
		return scheduledReminder{}, fmt.Errorf("the user already has %d reminders, the most allowed; tell them which ones they could cancel", count)
	}
	run, err := workflow.Start(ctx, workflows.Reminder{ChatID: u.ChatID, At: at, About: in.About, Person: in.Person, EveryDays: in.EveryDays})
	if err != nil {
		return scheduledReminder{}, err
	}
	reminder := store.Reminder{ID: run.ID(), RunID: run.ID(), At: at, About: in.About, Person: in.Person, EveryDays: in.EveryDays, CreatedAt: now}
	if _, err := workflow.Step(ctx, u.saveReminder, scoped[store.Reminder]{ChatID: u.ChatID, Input: reminder}); err != nil {
		return scheduledReminder{}, err
	}
	return scheduledReminder{ID: run.ID(), At: at.Format("Mon 2 Jan 2006 " + clock), EveryDays: in.EveryDays}, nil
}

func (f *Flows) saveReminder(ctx context.Context, input scoped[store.Reminder]) (struct{}, error) {
	return struct{}{}, f.Store.SaveReminder(ctx, input.ChatID, input.Input)
}

type reminderID struct {
	ID string `json:"id" desc:"The reminder's id."`
}

// cancelReminder deletes the reminder and cancels its current run. A run
// that has just handed off to the next still finds the reminder gone and
// ends without firing.
func (u userTools) cancelReminder(ctx context.Context, in reminderID) (string, error) {
	reminder, err := u.Store.Reminder(ctx, u.ChatID, in.ID)
	if errors.Is(err, store.ErrNotFound) {
		return "", workflow.FatalError(err)
	} else if err != nil {
		return "", err
	}
	run, err := workflow.Get[workflows.ReminderResult](ctx, cmp.Or(reminder.RunID, reminder.ID))
	if err != nil {
		return "", err
	}
	if err := run.Cancel(ctx); err != nil {
		return "", err
	}
	return "Cancelled.", u.Store.DeleteReminder(ctx, u.ChatID, in.ID)
}

type settingsInput struct {
	Name           string `json:"name,omitempty" desc:"What the user likes to be called."`
	Timezone       string `json:"timezone,omitempty" desc:"IANA time zone, for example Europe/London or Africa/Lagos."`
	WakeHour       *int   `json:"wakeHour,omitempty" desc:"Local hour (0-23) from which check-ins may arrive."`
	SleepHour      *int   `json:"sleepHour,omitempty" desc:"Local hour (0-23) after which check-ins stop."`
	CheckInsPerDay *int   `json:"checkInsPerDay,omitempty" desc:"How many times a day to check in first, 0 to 6."`
	Paused         *bool  `json:"paused,omitempty" desc:"True to pause check-ins, false to resume them. Reminders still fire."`
}

func (u userTools) updateSettings(ctx context.Context, in settingsInput) (store.User, error) {
	user, err := u.Store.User(ctx, u.ChatID)
	if err != nil {
		return store.User{}, err
	}
	if in.Name != "" {
		user.Name = in.Name
	}
	if in.Timezone != "" {
		if _, err := time.LoadLocation(in.Timezone); err != nil {
			return store.User{}, workflow.FatalError(fmt.Errorf("unknown time zone %q, use an IANA name such as Europe/London", in.Timezone))
		}
		user.Timezone = in.Timezone
	}
	for _, hour := range []struct {
		value *int
		field *int
	}{{in.WakeHour, &user.WakeHour}, {in.SleepHour, &user.SleepHour}} {
		if hour.value == nil {
			continue
		}
		if *hour.value < 0 || *hour.value > 23 {
			return store.User{}, workflow.FatalError(errors.New("hours are 0 to 23"))
		}
		*hour.field = *hour.value
	}
	if in.CheckInsPerDay != nil {
		user.CheckInsPerDay = min(max(*in.CheckInsPerDay, 0), 6)
	}
	if in.Paused != nil {
		user.Paused = *in.Paused
	}
	return user, u.Store.SaveUser(ctx, user)
}
