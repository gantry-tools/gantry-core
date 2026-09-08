package conversations

import (
	"errors"
	"sort"
	"strings"
)

type Event struct {
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Name      string `json:"name,omitempty"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	RunID     string `json:"runId,omitempty"`
}
type Record struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Workspace    string  `json:"workspace"`
	Provider     string  `json:"provider,omitempty"`
	Model        string  `json:"model,omitempty"`
	Session      string  `json:"openCodeSession,omitempty"`
	State        string  `json:"state,omitempty"`
	CurrentRunID string  `json:"currentRunId,omitempty"`
	CreatedAt    int64   `json:"createdAt"`
	UpdatedAt    int64   `json:"updatedAt"`
	ArchivedAt   int64   `json:"archivedAt,omitempty"`
	Events       []Event `json:"events"`
}

func ValidID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}
func HasControlCharacters(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}
func Validate(r Record) error {
	if !ValidID(r.ID) {
		return errors.New("invalid conversation id")
	}
	if len(r.Title) > 500 || len(r.Workspace) > 4096 || len(r.Session) > 256 || len(r.Events) > 2000 {
		return errors.New("conversation exceeds storage limits")
	}
	if HasControlCharacters(r.Title) || HasControlCharacters(r.Workspace) {
		return errors.New("conversation contains control characters")
	}
	for _, e := range r.Events {
		if e.Kind == "" || len(e.Text) > 1<<20 || len(e.Name) > 500 || e.RunID != "" && !ValidID(e.RunID) {
			return errors.New("invalid conversation event")
		}
	}
	return nil
}
func signature(e Event) string { return e.Kind + "\x00" + e.Text + "\x00" + e.Name }
func MergeEvents(server, client []Event) []Event {
	available := map[string]int{}
	serverSig := map[string]bool{}
	for _, e := range server {
		available[signature(e)]++
		serverSig[signature(e)] = true
	}
	out := append([]Event{}, server...)
	for _, e := range client {
		sig := signature(e)
		if available[sig] > 0 {
			available[sig]--
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return serverSig[signature(out[i])] && !serverSig[signature(out[j])]
	})
	return supersedeTerminals(supersedeReplacements(out))
}
func replacementRun(name string) string {
	if strings.HasPrefix(name, "repl:") && len(name) > 5 {
		return name[5:]
	}
	return ""
}
func terminalRun(name string) string {
	m := strings.SplitN(name, ":", 3)
	if len(m) == 3 && m[0] == "run" && m[1] != "" {
		return m[1]
	}
	return ""
}
func supersedeReplacements(events []Event) []Event {
	type repl struct{ text, run string }
	rs := []repl{}
	for _, e := range events {
		if e.Kind == "assistant" {
			if id := replacementRun(e.Name); id != "" {
				rs = append(rs, repl{strings.TrimSpace(e.Text), id})
			}
		}
	}
	drop := map[int]bool{}
	for i, e := range events {
		if e.Kind != "assistant" || replacementRun(e.Name) != "" || e.RunID == "" {
			continue
		}
		text := strings.TrimSpace(e.Text)
		for _, r := range rs {
			if r.run == e.RunID && r.text != text && strings.Contains(r.text, text) {
				drop[i] = true
				break
			}
		}
	}
	return without(events, drop)
}
func supersedeTerminals(events []Event) []Event {
	last := map[string]int{}
	for i, e := range events {
		if id := terminalRun(e.Name); id != "" {
			last[id] = i
		}
	}
	drop := map[int]bool{}
	for i, e := range events {
		if id := terminalRun(e.Name); id != "" && last[id] != i {
			drop[i] = true
		}
	}
	return without(events, drop)
}
func without(events []Event, drop map[int]bool) []Event {
	if len(drop) == 0 {
		return events
	}
	out := make([]Event, 0, len(events)-len(drop))
	for i, e := range events {
		if !drop[i] {
			out = append(out, e)
		}
	}
	return out
}
