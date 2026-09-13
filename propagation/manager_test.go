package propagation

import (
	"context"
	"testing"
	"time"
)

type stateMem struct {
	profiles map[string]Profile
	history  []HistoryRecord
}

func (s *stateMem) SaveProfile(_ context.Context, p Profile) error {
	if s.profiles == nil {
		s.profiles = map[string]Profile{}
	}
	s.profiles[p.ID] = p
	return nil
}
func (s *stateMem) DeleteProfile(_ context.Context, id string) error {
	delete(s.profiles, id)
	return nil
}
func (s *stateMem) ListProfiles(_ context.Context) ([]Profile, error) {
	out := []Profile{}
	for _, p := range s.profiles {
		out = append(out, p)
	}
	return out, nil
}
func (s *stateMem) RecordHistory(_ context.Context, h HistoryRecord) error {
	s.history = append(s.history, h)
	return nil
}
func (s *stateMem) ListHistory(_ context.Context, _ int) ([]HistoryRecord, error) {
	return append([]HistoryRecord(nil), s.history...), nil
}
func TestManagerProfiles(t *testing.T) {
	st := &stateMem{}
	m := Manager{Store: st, Now: func() time.Time { return time.Unix(1, 0) }}
	p := Profile{ID: "p", Name: "prod", Kinds: []string{"monitor"}, Mode: ReconcileNotify, Enabled: true}
	if err := m.SaveProfile(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	got, err := m.Profiles(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
}
