package propagation

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memState struct{ p []Profile }

func (m *memState) SaveProfile(_ context.Context, p Profile) error { m.p = []Profile{p}; return nil }
func (m *memState) DeleteProfile(context.Context, string) error    { return nil }
func (m *memState) ListProfiles(context.Context) ([]Profile, error) {
	return append([]Profile(nil), m.p...), nil
}
func (m *memState) RecordHistory(context.Context, HistoryRecord) error        { return nil }
func (m *memState) ListHistory(context.Context, int) ([]HistoryRecord, error) { return nil, nil }
func TestRunDueProfilesMarksRun(t *testing.T) {
	st := &memState{p: []Profile{{ID: "p", Name: "p", Kinds: []string{"x"}, Mode: ReconcileNotify, Enabled: true, Schedule: "1h"}}}
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	m := &Manager{Store: st, Now: func() time.Time { return now }}
	r, e := m.RunDueProfiles(context.Background(), func(context.Context, Profile) (ProfileRunResult, error) {
		return ProfileRunResult{Action: "notify", Drift: 1}, nil
	})
	if e != nil || len(r) != 1 || st.p[0].LastRunAt == nil {
		t.Fatalf("run=%#v p=%#v err=%v", r, st.p, e)
	}
}

func TestRunDueProfilesDoesNotAdvanceLastRunOnFailure(t *testing.T) {
	st := &memState{p: []Profile{{ID: "p", Name: "p", Kinds: []string{"x"}, Mode: ReconcileAutomatic, Enabled: true, Schedule: "1h"}}}
	now := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	m := &Manager{Store: st, Now: func() time.Time { return now }}
	r, e := m.RunDueProfiles(context.Background(), func(context.Context, Profile) (ProfileRunResult, error) {
		return ProfileRunResult{}, errors.New("member offline")
	})
	if e != nil {
		t.Fatalf("unexpected runner error: %v", e)
	}
	if len(r) != 1 || r[0].Error == "" {
		t.Fatalf("expected failed result, got %#v", r)
	}
	if st.p[0].LastRunAt != nil {
		t.Fatalf("last_run_at advanced after failed run: %v", st.p[0].LastRunAt)
	}
	due, err := Due(st.p[0], now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !due {
		t.Fatal("profile with failed run must remain due for retry")
	}
}
