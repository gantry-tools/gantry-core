package launcher

import "testing"

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
