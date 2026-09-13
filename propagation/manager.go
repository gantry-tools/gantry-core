package propagation

import (
	"context"
	"errors"
	"time"
)

type HistoryRecord struct {
	ID         string    `json:"id"`
	PlanID     string    `json:"plan_id"`
	Actor      string    `json:"actor"`
	Target     string    `json:"target"`
	Status     string    `json:"status"`
	Applied    int       `json:"applied"`
	Failed     int       `json:"failed"`
	RolledBack int       `json:"rolled_back"`
	Detail     string    `json:"detail,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type StateStore interface {
	SaveProfile(context.Context, Profile) error
	DeleteProfile(context.Context, string) error
	ListProfiles(context.Context) ([]Profile, error)
	RecordHistory(context.Context, HistoryRecord) error
	ListHistory(context.Context, int) ([]HistoryRecord, error)
}

type Manager struct {
	Adapter LocalAdapter
	Store   StateStore
	Now     func() time.Time
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}
func (m *Manager) Export(ctx context.Context, kinds []string, actor Actor, target string) ([]Envelope, error) {
	if m.Adapter == nil {
		return nil, errors.New("propagation adapter required")
	}
	return m.Adapter.Export(ctx, kinds, actor, target)
}
func (m *Manager) Preview(ctx context.Context, source []Envelope, actor Actor) (Preview, error) {
	return PreviewLocal(ctx, m.Adapter, source, actor, false)
}
func (m *Manager) Apply(ctx context.Context, planID string, source []Envelope, actor Actor, target string) (ApplyBundleResult, error) {
	r, err := ApplyLocal(ctx, m.Adapter, source, actor)
	if m.Store != nil {
		h := HistoryRecord{ID: planID + "-local", PlanID: planID, Actor: actor.Kind + ":" + actor.ID, Target: target, CreatedAt: m.now()}
		for _, v := range r.Revisions {
			_ = v
			h.Applied++
		}
		if err != nil {
			h.Failed = 1
			h.Status = "failed"
			h.Detail = r.Error
		} else {
			h.Status = "applied"
		}
		if r.RolledBack {
			h.RolledBack = h.Applied
			h.Status = "rolled-back"
		}
		_ = m.Store.RecordHistory(ctx, h)
	}
	return r, err
}
func (m *Manager) SaveProfile(ctx context.Context, p Profile) error {
	if m.Store == nil {
		return errors.New("propagation state store required")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	return m.Store.SaveProfile(ctx, p)
}
func (m *Manager) Profiles(ctx context.Context) ([]Profile, error) {
	if m.Store == nil {
		return nil, errors.New("propagation state store required")
	}
	return m.Store.ListProfiles(ctx)
}
func (m *Manager) History(ctx context.Context, limit int) ([]HistoryRecord, error) {
	if m.Store == nil {
		return nil, errors.New("propagation state store required")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return m.Store.ListHistory(ctx, limit)
}
