package propagation

import (
	"errors"
	"sort"
	"strings"
)

type Member struct {
	ID           string
	Labels       []string
	Capabilities []string
	Enabled      bool
}
type Selector struct {
	Nodes        []string `json:"nodes,omitempty"`
	Labels       []string `json:"labels,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Exclude      []string `json:"exclude,omitempty"`
	All          bool     `json:"all,omitempty"`
}

func Select(s Selector, members []Member) ([]string, error) {
	if !s.All && len(s.Nodes) == 0 && len(s.Labels) == 0 && len(s.Capabilities) == 0 {
		return nil, errors.New("target selector is empty")
	}
	want := map[string]bool{}
	ex := map[string]bool{}
	for _, x := range s.Exclude {
		ex[x] = true
	}
	nodes := map[string]bool{}
	for _, x := range s.Nodes {
		nodes[x] = true
	}
	for _, m := range members {
		if !m.Enabled || ex[m.ID] {
			continue
		}
		match := s.All || nodes[m.ID] || hasAny(m.Labels, s.Labels) || hasAll(m.Capabilities, s.Capabilities)
		if match {
			want[m.ID] = true
		}
	}
	out := make([]string, 0, len(want))
	for id := range want {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
func hasAny(have, want []string) bool {
	if len(want) == 0 {
		return false
	}
	m := map[string]bool{}
	for _, x := range have {
		m[strings.ToLower(x)] = true
	}
	for _, x := range want {
		if m[strings.ToLower(x)] {
			return true
		}
	}
	return false
}
func hasAll(have, want []string) bool {
	if len(want) == 0 {
		return false
	}
	m := map[string]bool{}
	for _, x := range have {
		m[strings.ToLower(x)] = true
	}
	for _, x := range want {
		if !m[strings.ToLower(x)] {
			return false
		}
	}
	return true
}
