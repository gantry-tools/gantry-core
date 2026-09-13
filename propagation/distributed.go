package propagation

import (
	"context"
	"sort"
	"sync"
)

type RemotePreviewFunc func(context.Context, string, []Envelope, Actor) (Preview, error)
type RemoteApplyFunc func(context.Context, string, string, []Envelope, Actor) (ApplyBundleResult, error)

type NodePreview struct {
	Node    string  `json:"node"`
	Preview Preview `json:"preview"`
	Error   string  `json:"error,omitempty"`
}
type NodeApply struct {
	Node   string            `json:"node"`
	Result ApplyBundleResult `json:"result"`
	Error  string            `json:"error,omitempty"`
}

func PreviewNodes(ctx context.Context, nodes []string, source []Envelope, actor Actor, limit int, fn RemotePreviewFunc) []NodePreview {
	if limit < 1 {
		limit = 4
	}
	sem := make(chan struct{}, limit)
	out := make([]NodePreview, len(nodes))
	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Add(1)
		go func(i int, node string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				out[i] = NodePreview{Node: node, Error: ctx.Err().Error()}
				return
			}
			defer func() { <-sem }()
			v, err := fn(ctx, node, source, actor)
			out[i] = NodePreview{Node: node, Preview: v}
			if err != nil {
				out[i].Error = err.Error()
			}
		}(i, node)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}
func ApplyNodes(ctx context.Context, nodes []string, planID string, source []Envelope, actor Actor, limit int, fn RemoteApplyFunc) []NodeApply {
	if limit < 1 {
		limit = 4
	}
	sem := make(chan struct{}, limit)
	out := make([]NodeApply, len(nodes))
	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Add(1)
		go func(i int, node string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				out[i] = NodeApply{Node: node, Error: ctx.Err().Error()}
				return
			}
			defer func() { <-sem }()
			v, err := fn(ctx, node, planID, source, actor)
			out[i] = NodeApply{Node: node, Result: v}
			if err != nil {
				out[i].Error = err.Error()
			}
		}(i, node)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}
