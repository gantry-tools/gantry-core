package propagation

import (
	"context"
	"errors"
	"testing"
)

func TestDistributedPartialFailureVisible(t *testing.T) {
	nodes := []string{"b", "a"}
	r := PreviewNodes(context.Background(), nodes, nil, Actor{}, 2, func(_ context.Context, n string, _ []Envelope, _ Actor) (Preview, error) {
		if n == "b" {
			return Preview{}, errors.New("offline")
		}
		return Preview{Applicable: true}, nil
	})
	if len(r) != 2 || r[0].Node != "a" || !r[0].Preview.Applicable || r[1].Error == "" {
		t.Fatalf("partial result %#v", r)
	}
}
