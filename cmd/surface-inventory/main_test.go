package main

import "testing"

func TestKeysAreStable(t *testing.T) {
	got := keys(map[string]bool{"z": true, "a": true})
	if len(got) != 2 || got[0] != "a" || got[1] != "z" {
		t.Fatalf("keys = %#v", got)
	}
}

func TestAddMergesEvidence(t *testing.T) {
	all := map[string]*found{}
	add(all, "/api/items", "GET", "server.go", false)
	add(all, "/api/items", "POST", "app.js", true)
	got := all["/api/items"]
	if !got.methods["GET"] || !got.methods["POST"] || !got.website || len(got.sources) != 2 {
		t.Fatalf("merged finding = %#v", got)
	}
}
