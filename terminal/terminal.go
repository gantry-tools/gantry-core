package terminal

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	DefaultTitle           = "Terminal"
	DefaultState           = "disconnected"
	MaxTitleBytes          = 200
	MaxCWDBytes            = 4096
	DefaultScrollbackLimit = 256 << 10
	DefaultSessionLimit    = 16
)

type Session struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	CWD        string `json:"cwd"`
	State      string `json:"state"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
	ClosedAt   int64  `json:"closedAt,omitempty"`
	Scrollback string `json:"scrollback,omitempty"`
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
func Normalize(s Session) (Session, error) {
	s.ID = strings.TrimSpace(s.ID)
	if !ValidID(s.ID) || len(s.Title) > MaxTitleBytes || len(s.CWD) > MaxCWDBytes {
		return Session{}, errors.New("invalid terminal session")
	}
	if strings.TrimSpace(s.Title) == "" {
		s.Title = DefaultTitle
	}
	if s.State == "" {
		s.State = DefaultState
	}
	return s, nil
}
func AppendScrollback(existing string, p []byte, limit int) string {
	if limit <= 0 {
		limit = DefaultScrollbackLimit
	}
	combined := []byte(existing + strings.ToValidUTF8(string(p), "�"))
	if len(combined) <= limit {
		return string(combined)
	}
	combined = combined[len(combined)-limit:]
	for len(combined) > 0 && !utf8.RuneStart(combined[0]) {
		combined = combined[1:]
	}
	return string(combined)
}

func NormalizeOutput(p []byte) []byte {
	return []byte(strings.ToValidUTF8(string(p), "�"))
}
func CheckCapacity(exists bool, count, limit int) error {
	if limit <= 0 {
		limit = DefaultSessionLimit
	}
	if !exists && count >= limit {
		return errors.New("terminal session limit reached")
	}
	return nil
}
