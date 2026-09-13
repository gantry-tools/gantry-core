package cluster

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
)

type TargetKind string

const (
	TargetLocal   TargetKind = "local"
	TargetNode    TargetKind = "node"
	TargetMembers TargetKind = "members"
	TargetAll     TargetKind = "all"
)

type Target struct {
	Kind   TargetKind `json:"kind"`
	NodeID string     `json:"node_id,omitempty"`
}

func ParseTarget(raw string) (Target, error) {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "", "all":
		return Target{Kind: TargetAll}, nil
	case "local":
		return Target{Kind: TargetLocal}, nil
	case "members":
		return Target{Kind: TargetMembers}, nil
	}
	if strings.HasPrefix(raw, "node:") && len(strings.TrimPrefix(raw, "node:")) >= 4 {
		return Target{Kind: TargetNode, NodeID: strings.TrimPrefix(raw, "node:")}, nil
	}
	if len(raw) >= 4 {
		return Target{Kind: TargetNode, NodeID: raw}, nil
	}
	return Target{}, errors.New("invalid cluster target")
}

type NodeResult[T any] struct {
	NodeID    string `json:"node_id"`
	OwnerNode string `json:"owner_node"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Value     *T     `json:"value,omitempty"`
}
type Report[T any] struct {
	RequestID string          `json:"request_id"`
	Partial   bool            `json:"partial"`
	Results   []NodeResult[T] `json:"results"`
}

func FanOut[T any](ctx context.Context, nodeIDs []string, limit int, fn func(context.Context, string) (T, error)) []NodeResult[T] {
	if limit <= 0 {
		limit = 4
	}
	ids := append([]string(nil), nodeIDs...)
	sort.Strings(ids)
	results := make([]NodeResult[T], 0, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, limit)
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			v, err := fn(ctx, id)
			r := NodeResult[T]{NodeID: id, OwnerNode: id}
			if err != nil {
				r.Error = err.Error()
			} else {
				r.OK = true
				r.Value = &v
			}
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].NodeID < results[j].NodeID })
	return results
}

func IsPartial[T any](results []NodeResult[T]) bool {
	for _, r := range results {
		if !r.OK {
			return true
		}
	}
	return false
}
