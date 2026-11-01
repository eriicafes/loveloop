// Package store keeps LoveLoop's users, their loved ones, their reminders,
// and their conversation history in Redis.
package store

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/eriicafes/go-workflow/workflowai"
	"github.com/eriicafes/got"
	"github.com/redis/go-redis/v9"
)

// HistoryLimit is how many recent messages of a conversation are sent to the
// model. Older ones are folded into the conversation's Summary.
const HistoryLimit = 60

// ErrNotFound is returned when a user or a loved one does not exist.
var ErrNotFound = errors.New("not found")

// User is someone chatting with the bot.
type User struct {
	ChatID   int64  `json:"chatId"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
	// WakeHour and SleepHour bound the local hours the bot texts first in.
	WakeHour  int `json:"wakeHour"`
	SleepHour int `json:"sleepHour"`
	// CheckInsPerDay is how many times a day the bot texts first.
	CheckInsPerDay int       `json:"checkInsPerDay"`
	Paused         bool      `json:"paused"`
	CreatedAt      time.Time `json:"createdAt"`
	// LastMessageAt is when the user last texted, so check-ins don't
	// interrupt a conversation.
	LastMessageAt time.Time `json:"lastMessageAt,omitzero"`
}

// Location returns the user's time zone, or UTC when it is unknown.
func (u User) Location() *time.Location {
	if loc, err := time.LoadLocation(u.Timezone); err == nil && u.Timezone != "" {
		return loc
	}
	return time.UTC
}

// Person is one of the user's loved ones.
type Person struct {
	Name            string    `json:"name"`
	Relationship    string    `json:"relationship,omitempty"`
	Nickname        string    `json:"nickname,omitempty"`
	Birthday        string    `json:"birthday,omitempty"`
	Anniversary     string    `json:"anniversary,omitempty"`
	Phone           string    `json:"phone,omitempty"`
	TelegramHandle  string    `json:"telegramHandle,omitempty"`
	LoveLanguage    string    `json:"loveLanguage,omitempty"`
	Likes           []string  `json:"likes,omitempty"`
	Dislikes        []string  `json:"dislikes,omitempty"`
	Notes           []string  `json:"notes,omitempty"`
	KeepInTouchDays int       `json:"keepInTouchDays,omitempty"`
	LastContactAt   time.Time `json:"lastContactAt,omitzero"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// Key is the name a person is stored under.
func (p Person) Key() string { return PersonKey(p.Name) }

// PersonKey normalizes a name into the key a person is stored under.
func PersonKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Reminder is a reminder the user asked for. Its ID is the ID of the run
// that delivers it.
type Reminder struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	About     string    `json:"about"`
	Person    string    `json:"person,omitempty"`
	EveryDays int       `json:"everyDays,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// Store reads and writes LoveLoop's data.
type Store struct {
	rdb    redis.UniversalClient
	prefix string
}

// New returns a Store on rdb whose keys start with prefix.
func New(rdb redis.UniversalClient, prefix string) *Store {
	return &Store{rdb: rdb, prefix: prefix}
}

// GetStore returns the Store on REDIS_URL, with keys under
// LOVELOOP_REDIS_PREFIX or loveloop.
var GetStore = got.TryUsing(func(c *got.Container) (*Store, error) {
	options, err := redis.ParseURL(os.Getenv("REDIS_URL"))
	if err != nil {
		return nil, fmt.Errorf("REDIS_URL: %w", err)
	}
	return New(redis.NewClient(options), cmp.Or(os.Getenv("LOVELOOP_REDIS_PREFIX"), "loveloop")), nil
})

func (s *Store) key(parts ...any) string {
	var b strings.Builder
	b.WriteString(s.prefix)
	for _, part := range parts {
		fmt.Fprintf(&b, ":%v", part)
	}
	return b.String()
}

// EnsureUser returns the user of chatID, creating it with defaults when it is
// new, and reports whether it was created. It records now as the time of the
// user's last message.
func (s *Store) EnsureUser(ctx context.Context, chatID int64, name string, now time.Time) (User, bool, error) {
	user := User{ChatID: chatID, Name: name, Timezone: "UTC", WakeHour: 9, SleepHour: 21, CheckInsPerDay: 2, CreatedAt: now, LastMessageAt: now}
	data, err := json.Marshal(user)
	if err != nil {
		return User{}, false, err
	}
	created, err := s.rdb.SetNX(ctx, s.key("user", chatID), data, 0).Result()
	if err != nil {
		return User{}, false, err
	}
	if created {
		return user, true, s.rdb.SAdd(ctx, s.key("users"), chatID).Err()
	}
	user, err = s.User(ctx, chatID)
	if err != nil {
		return User{}, false, err
	}
	user.LastMessageAt = now
	return user, false, s.SaveUser(ctx, user)
}

// User returns the user of chatID.
func (s *Store) User(ctx context.Context, chatID int64) (User, error) {
	var user User
	return user, s.getJSON(ctx, s.key("user", chatID), &user)
}

// SaveUser replaces a user.
func (s *Store) SaveUser(ctx context.Context, user User) error {
	return s.setJSON(ctx, s.key("user", user.ChatID), user)
}

// Users returns the chat IDs of every user.
func (s *Store) Users(ctx context.Context) ([]int64, error) {
	members, err := s.rdb.SMembers(ctx, s.key("users")).Result()
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(members))
	for _, member := range members {
		id, err := strconv.ParseInt(member, 10, 64)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

// CheckInsRun returns the ID of the run that carries the user's check-ins,
// or "" when none was started.
func (s *Store) CheckInsRun(ctx context.Context, chatID int64) (string, error) {
	id, err := s.rdb.Get(ctx, s.key("checkins", chatID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return id, err
}

// SetCheckInsRun records the run that carries the user's check-ins.
func (s *Store) SetCheckInsRun(ctx context.Context, chatID int64, runID string) error {
	return s.rdb.Set(ctx, s.key("checkins", chatID), runID, 0).Err()
}

// People returns the user's loved ones sorted by name.
func (s *Store) People(ctx context.Context, chatID int64) ([]Person, error) {
	values, err := s.rdb.HGetAll(ctx, s.key("people", chatID)).Result()
	if err != nil {
		return nil, err
	}
	people := make([]Person, 0, len(values))
	for _, value := range values {
		var person Person
		if err := json.Unmarshal([]byte(value), &person); err != nil {
			return nil, err
		}
		people = append(people, person)
	}
	slices.SortFunc(people, func(a, b Person) int { return strings.Compare(a.Key(), b.Key()) })
	return people, nil
}

// Person returns one of the user's loved ones by name.
func (s *Store) Person(ctx context.Context, chatID int64, name string) (Person, error) {
	value, err := s.rdb.HGet(ctx, s.key("people", chatID), PersonKey(name)).Result()
	if errors.Is(err, redis.Nil) {
		return Person{}, fmt.Errorf("%w: no loved one named %q", ErrNotFound, name)
	} else if err != nil {
		return Person{}, err
	}
	var person Person
	return person, json.Unmarshal([]byte(value), &person)
}

// SavePerson creates or replaces a loved one.
func (s *Store) SavePerson(ctx context.Context, chatID int64, person Person) error {
	data, err := json.Marshal(person)
	if err != nil {
		return err
	}
	return s.rdb.HSet(ctx, s.key("people", chatID), person.Key(), data).Err()
}

// DeletePerson forgets a loved one.
func (s *Store) DeletePerson(ctx context.Context, chatID int64, name string) error {
	removed, err := s.rdb.HDel(ctx, s.key("people", chatID), PersonKey(name)).Result()
	if err == nil && removed == 0 {
		return fmt.Errorf("%w: no loved one named %q", ErrNotFound, name)
	}
	return err
}

// Reminders returns the user's reminders, soonest first.
func (s *Store) Reminders(ctx context.Context, chatID int64) ([]Reminder, error) {
	values, err := s.rdb.HGetAll(ctx, s.key("reminders", chatID)).Result()
	if err != nil {
		return nil, err
	}
	reminders := make([]Reminder, 0, len(values))
	for _, value := range values {
		var reminder Reminder
		if err := json.Unmarshal([]byte(value), &reminder); err != nil {
			return nil, err
		}
		reminders = append(reminders, reminder)
	}
	slices.SortFunc(reminders, func(a, b Reminder) int { return a.At.Compare(b.At) })
	return reminders, nil
}

// ReminderCount returns how many reminders the user has.
func (s *Store) ReminderCount(ctx context.Context, chatID int64) (int64, error) {
	return s.rdb.HLen(ctx, s.key("reminders", chatID)).Result()
}

// Reminder returns one reminder, or ErrNotFound when it was cancelled or
// has fired for the last time.
func (s *Store) Reminder(ctx context.Context, chatID int64, id string) (Reminder, error) {
	value, err := s.rdb.HGet(ctx, s.key("reminders", chatID), id).Result()
	if errors.Is(err, redis.Nil) {
		return Reminder{}, fmt.Errorf("%w: no reminder %q", ErrNotFound, id)
	} else if err != nil {
		return Reminder{}, err
	}
	var reminder Reminder
	return reminder, json.Unmarshal([]byte(value), &reminder)
}

// SaveReminder creates or replaces a reminder.
func (s *Store) SaveReminder(ctx context.Context, chatID int64, reminder Reminder) error {
	data, err := json.Marshal(reminder)
	if err != nil {
		return err
	}
	return s.rdb.HSet(ctx, s.key("reminders", chatID), reminder.ID, data).Err()
}

// DeleteReminder removes a reminder.
func (s *Store) DeleteReminder(ctx context.Context, chatID int64, id string) error {
	return s.rdb.HDel(ctx, s.key("reminders", chatID), id).Err()
}

// History returns the end of the user's conversation. It starts at a user
// message, so it never begins in the middle of a tool call.
func (s *Store) History(ctx context.Context, chatID int64) ([]workflowai.ModelMessage, error) {
	values, err := s.rdb.LRange(ctx, s.key("history", chatID), -HistoryLimit, -1).Result()
	if err != nil {
		return nil, err
	}
	messages, err := decodeMessages(values)
	if err != nil {
		return nil, err
	}
	start := slices.IndexFunc(messages, func(m workflowai.ModelMessage) bool { return m.Role == workflowai.RoleUser })
	if start < 0 {
		return nil, nil
	}
	return messages[start:], nil
}

// FullHistory returns every stored message of the user's conversation,
// oldest first.
func (s *Store) FullHistory(ctx context.Context, chatID int64) ([]workflowai.ModelMessage, error) {
	values, err := s.rdb.LRange(ctx, s.key("history", chatID), 0, -1).Result()
	if err != nil {
		return nil, err
	}
	return decodeMessages(values)
}

func decodeMessages(values []string) ([]workflowai.ModelMessage, error) {
	messages := make([]workflowai.ModelMessage, 0, len(values))
	for _, value := range values {
		var message workflowai.ModelMessage
		if err := json.Unmarshal([]byte(value), &message); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

// AppendHistory adds messages to the end of the user's conversation in one
// write, so the messages of a turn stay together, and trims old messages.
func (s *Store) AppendHistory(ctx context.Context, chatID int64, messages []workflowai.ModelMessage) error {
	if len(messages) == 0 {
		return nil
	}
	values := make([]any, len(messages))
	for i, message := range messages {
		data, err := json.Marshal(message)
		if err != nil {
			return err
		}
		values[i] = data
	}
	key := s.key("history", chatID)
	_, err := s.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.RPush(ctx, key, values...)
		pipe.LTrim(ctx, key, -2*HistoryLimit, -1)
		return nil
	})
	return err
}

// ClearHistory forgets the user's conversation and its summary.
func (s *Store) ClearHistory(ctx context.Context, chatID int64) error {
	return s.rdb.Del(ctx, s.key("history", chatID), s.key("summary", chatID)).Err()
}

// Summary is the running summary of the older part of a conversation,
// whose messages were folded out of the history.
type Summary struct {
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Summary returns the user's conversation summary, which is empty until the
// history is first compacted.
func (s *Store) Summary(ctx context.Context, chatID int64) (Summary, error) {
	var summary Summary
	err := s.getJSON(ctx, s.key("summary", chatID), &summary)
	if errors.Is(err, ErrNotFound) {
		return Summary{}, nil
	}
	return summary, err
}

// CompactHistory replaces the first n messages of the user's conversation
// with summary, which must cover them. first is the oldest of them as it was
// summarized: when another write has changed the start of the history since,
// nothing changes and CompactHistory reports false.
func (s *Store) CompactHistory(ctx context.Context, chatID int64, n int, first workflowai.ModelMessage, summary Summary) (bool, error) {
	key := s.key("history", chatID)
	data, err := json.Marshal(summary)
	if err != nil {
		return false, err
	}
	compacted := false
	err = s.rdb.Watch(ctx, func(tx *redis.Tx) error {
		value, err := tx.LIndex(ctx, key, 0).Result()
		if errors.Is(err, redis.Nil) {
			return nil
		} else if err != nil {
			return err
		}
		var current workflowai.ModelMessage
		if err := json.Unmarshal([]byte(value), &current); err != nil {
			return err
		}
		// Compare the decoded messages, since first was decoded too.
		if !reflect.DeepEqual(current, first) {
			return nil
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, s.key("summary", chatID), data, 0)
			pipe.LTrim(ctx, key, int64(n), -1)
			return nil
		})
		compacted = err == nil
		return err
	}, key)
	if errors.Is(err, redis.TxFailedErr) {
		return false, nil
	}
	return compacted, err
}

// Mail is one item waiting in a user's mailbox for their conversation run:
// a message they sent, their answer to an approval question, or a reason
// for LoveLoop to text first.
type Mail struct {
	Text string `json:"text,omitempty"`
	// ApprovalKey and Approved answer a pending tool approval.
	ApprovalKey string `json:"approvalKey,omitempty"`
	Approved    bool   `json:"approved,omitempty"`
	// Nudge is why LoveLoop texts first, from a check-in or a reminder.
	Nudge string `json:"nudge,omitempty"`
}

// MailBatch is the most mail PeekMail returns at once.
const MailBatch = 20

// PushMail adds mail to the end of the user's mailbox.
func (s *Store) PushMail(ctx context.Context, chatID int64, mail Mail) error {
	data, err := json.Marshal(mail)
	if err != nil {
		return err
	}
	return s.rdb.RPush(ctx, s.key("mailbox", chatID), data).Err()
}

// PeekMail returns up to MailBatch of the oldest mail in the user's mailbox
// without removing it. AckMail removes it once it is handled.
func (s *Store) PeekMail(ctx context.Context, chatID int64) ([]Mail, error) {
	values, err := s.rdb.LRange(ctx, s.key("mailbox", chatID), 0, MailBatch-1).Result()
	if err != nil {
		return nil, err
	}
	mails := make([]Mail, len(values))
	for i, value := range values {
		if err := json.Unmarshal([]byte(value), &mails[i]); err != nil {
			return nil, err
		}
	}
	return mails, nil
}

// AckMail removes the n oldest mail from the user's mailbox. Mail is only
// added at the end, so these are the ones PeekMail returned.
func (s *Store) AckMail(ctx context.Context, chatID int64, n int) error {
	return s.rdb.LTrim(ctx, s.key("mailbox", chatID), int64(n), -1).Err()
}

// MailCount returns how much mail waits in the user's mailbox.
func (s *Store) MailCount(ctx context.Context, chatID int64) (int64, error) {
	return s.rdb.LLen(ctx, s.key("mailbox", chatID)).Result()
}

// Once reports whether key is claimed for the first time within ttl. It
// deduplicates Telegram updates and scheduled runs.
func (s *Store) Once(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return s.rdb.SetNX(ctx, s.key("once", key), 1, ttl).Result()
}

// Release releases a key claimed with Once, so a failed attempt can be
// retried.
func (s *Store) Release(ctx context.Context, key string) error {
	return s.rdb.Del(ctx, s.key("once", key)).Err()
}

func (s *Store) getJSON(ctx context.Context, key string, v any) error {
	value, err := s.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	} else if err != nil {
		return err
	}
	return json.Unmarshal([]byte(value), v)
}

func (s *Store) setJSON(ctx context.Context, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, key, data, 0).Err()
}
