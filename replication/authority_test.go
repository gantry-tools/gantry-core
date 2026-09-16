package replication

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestAuthorityRouteMatrix proves the product-neutral authoritative mutation
// routing matrix: standalone -> ErrStandalone; ready leader -> propose; ready
// follower -> forward; any other replicated readiness -> reject. It uses
// arbitrary product functions (no Watchpost concepts).
func TestAuthorityRouteMatrix(t *testing.T) {
	propose := func(ctx context.Context) (*ApplyResult, error) { return &ApplyResult{Index: 1}, nil }
	forward := func(ctx context.Context) (*ApplyResult, error) { return &ApplyResult{Index: 2}, nil }
	rejectCases := []Readiness{ReadinessStarting, ReadinessLearner, ReadinessNoLeader, ReadinessUnhealthy, ReadinessShuttingDown}

	a := NewAuthority()
	if _, err := a.Route(context.Background(), propose, forward); !errors.Is(err, ErrStandalone) {
		t.Fatalf("standalone must return ErrStandalone, got %v", err)
	}

	a.SetMode(ModeReplicated)
	a.SetReadiness(ReadinessReadyLeader)
	res, err := a.Route(context.Background(), propose, forward)
	if err != nil || res.Index != 1 {
		t.Fatalf("ready-leader must propose: %v %+v", err, res)
	}
	a.SetReadiness(ReadinessReadyFollower)
	res, err = a.Route(context.Background(), propose, forward)
	if err != nil || res.Index != 2 {
		t.Fatalf("ready-follower must forward: %v %+v", err, res)
	}
	for _, rd := range rejectCases {
		a.SetReadiness(rd)
		if _, err := a.Route(context.Background(), propose, forward); err == nil {
			t.Fatalf("readiness %s must reject", rd)
		}
	}
	if got, _ := a.State(); got != ModeReplicated {
		t.Fatalf("mode=%s want replicated", got)
	}
}

// TestFakeConsumerNoWatchpostImports proves the extracted runtime does not
// require any product concept: a fake product executor uses an arbitrary
// operation kind and the generic request-identity propagation.
func TestFakeConsumerNoWatchpostImports(t *testing.T) {
	ctx := context.Background()
	// A fake product's arbitrary semantic payload.
	payload := json.RawMessage(`{"collection":"c1","record":"r1","value":42}`)
	fr := ForwardRequest{Kind: "collection.record.set", ObjectID: "r1", OpID: "fake-req-1", Revision: 0, Payload: payload}

	a := NewAuthority()
	a.SetMode(ModeReplicated)
	a.SetReadiness(ReadinessReadyFollower)
	a.SetForwardClient(func(ctx context.Context, req ForwardRequest) (*ApplyResult, error) {
		if req.OpID != "fake-req-1" || req.Kind != "collection.record.set" {
			return nil, errors.New("envelope altered in transit")
		}
		return &ApplyResult{Index: 9, OpID: req.OpID, ObjectID: req.ObjectID, Kind: req.Kind}, nil
	})

	// Request identity propagation through the forwarding envelope.
	ctx = WithRequestID(ctx, "fake-req-1")
	got, err := a.Route(ctx,
		func(ctx context.Context) (*ApplyResult, error) { return nil, errors.New("must not propose") },
		func(ctx context.Context) (*ApplyResult, error) {
			if RequestID(ctx) != "fake-req-1" {
				return nil, errors.New("request identity lost")
			}
			return a.ForwardClient()(ctx, fr)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.OpID != "fake-req-1" || got.Kind != "collection.record.set" {
		t.Fatalf("unexpected forwarded result: %+v", got)
	}
	if RequestID(context.Background()) != "" {
		t.Fatal("request identity must be empty on a fresh context")
	}
}
