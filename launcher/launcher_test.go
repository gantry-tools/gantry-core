package launcher

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteAccessError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		response := httptest.NewRecorder()
		WriteAccessError(response, status, "Warden", "W", "#4ecb71")
		if response.Code != status || response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Fatalf("status %d produced %d %q", status, response.Code, response.Header().Get("Content-Type"))
		}
		body := response.Body.String()
		if !strings.Contains(body, "Warden") || !strings.Contains(body, "/app/?return=%2F%3Fconfig") || !strings.Contains(body, "--sorbet-red:#f38f92") || !strings.Contains(body, "--accent:#4ecb71") {
			t.Fatalf("status %d missing useful error content: %q", status, body)
		}
	}
}

func TestPortableDocumentAndAppURLs(t *testing.T) {
	port := 7331
	items, err := Normalize("cortex", Document{Version: 1, Product: "cortex", Instances: []Instance{{Name: "Local", Domain: "localhost", Port: &port}, {Name: "Hosted", Domain: "https://cortex.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	view, err := MakeView("cortex", items)
	if err != nil {
		t.Fatal(err)
	}
	if view.Instances[0].AppURL != "http://localhost:7331/app/" || view.Instances[1].AppURL != "https://cortex.example/app/" {
		t.Fatalf("unexpected URLs: %#v", view.Instances)
	}
}

func TestRejectsCrossProductAndPaths(t *testing.T) {
	for _, document := range []Document{
		{Version: 1, Product: "warden", Instances: []Instance{}},
		{Version: 1, Product: "cortex", Instances: []Instance{{Name: "bad", Domain: "example.com/path"}}},
	} {
		if _, err := Normalize("cortex", document); err == nil {
			t.Fatalf("accepted %#v", document)
		}
	}
}
