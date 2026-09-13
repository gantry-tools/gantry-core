// Command surface-inventory records the currently observable HTTP, browser and
// CLI surfaces of the Gantry Go applications. It is deliberately a baseline
// scanner rather than a source generator: later checkpoints replace inferred
// metadata with executable operation declarations while retaining this report
// as evidence of what existed before adoption.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type project struct {
	Name string
	Path string
}

type endpoint struct {
	Method          string   `json:"method"`
	Path            string   `json:"path"`
	Sources         []string `json:"sources"`
	WebsiteConsumer bool     `json:"website_consumer"`
	CLI             bool     `json:"cli"`
	Classification  string   `json:"classification"`
	Automation      string   `json:"automation"`
}

type report struct {
	SchemaVersion int        `json:"schema_version"`
	Project       string     `json:"project"`
	Repository    string     `json:"repository"`
	Evidence      []string   `json:"evidence"`
	CLICommands   []string   `json:"cli_commands"`
	Endpoints     []endpoint `json:"endpoints"`
	Notes         []string   `json:"notes"`
}

type found struct {
	methods map[string]bool
	sources map[string]bool
	website bool
}

var (
	patternRoute = regexp.MustCompile(`\"(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+(/[^\"]*)\"`)
	pathRoute    = regexp.MustCompile(`(?:public|session|capability)\(\"(/api/[^\"]+)\"`)
	apiLiteral   = regexp.MustCompile("[\\\"'`](/(?:api|admin|system)/[A-Za-z0-9_./{}:-]*)[\\\"'`]")
	commandCase  = regexp.MustCompile(`case\s+\"([a-z][a-z0-9-]*)\"`)
)

func main() {
	workspace := flag.String("workspace", "..", "directory containing the Gantry repositories")
	out := flag.String("out", "docs/inventory", "output directory")
	flag.Parse()
	projects := []project{
		{"cortex", "crtx-dev/cortex"},
		{"warden", "warden-cv/warden"},
		{"trestle", "trestle-cv/trestle"},
		{"watchpost", "watchpost-cv/watchpost"},
		{"watchpost-agent", "watchpost-cv/watchpost-agent"},
		{"webfleet", "webfleet-cv/webfleet"},
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		fatal(err)
	}
	for _, p := range projects {
		r, err := inspect(filepath.Join(*workspace, p.Path), p)
		if err != nil {
			fatal(fmt.Errorf("%s: %w", p.Name, err))
		}
		body, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			fatal(err)
		}
		body = append(body, '\n')
		if err := os.WriteFile(filepath.Join(*out, p.Name+".json"), body, 0644); err != nil {
			fatal(err)
		}
	}
}

func inspect(root string, p project) (report, error) {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return report{}, fmt.Errorf("repository not found at %s", root)
	}
	endpoints := map[string]*found{}
	commands := map[string]bool{}
	evidence := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendor" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".js" && ext != ".html" {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") || strings.Contains(filepath.ToSlash(path), "/tests/") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		isWebsite := ext == ".js" || ext == ".html"
		for _, match := range patternRoute.FindAllSubmatch(body, -1) {
			add(endpoints, string(match[2]), string(match[1]), rel, isWebsite)
			evidence[rel] = true
		}
		for _, match := range pathRoute.FindAllSubmatch(body, -1) {
			add(endpoints, string(match[1]), "ANY", rel, isWebsite)
			evidence[rel] = true
		}
		for _, match := range apiLiteral.FindAllSubmatch(body, -1) {
			pathValue := string(match[1])
			if strings.HasSuffix(pathValue, "/") || pathValue == "/api/" {
				continue
			}
			add(endpoints, pathValue, "ANY", rel, isWebsite)
			if isWebsite {
				evidence[rel] = true
			}
		}
		if ext == ".go" && (strings.Contains(rel, "/cmd/") || strings.HasPrefix(rel, "cmd/")) {
			for _, match := range commandCase.FindAllSubmatch(body, -1) {
				commands[string(match[1])] = true
			}
		}
		return nil
	})
	if err != nil {
		return report{}, err
	}
	rows := make([]endpoint, 0)
	for pathValue, f := range endpoints {
		methods := keys(f.methods)
		if len(methods) > 1 {
			filtered := methods[:0]
			for _, method := range methods {
				if method != "ANY" {
					filtered = append(filtered, method)
				}
			}
			methods = filtered
		}
		for _, method := range methods {
			classification := "read"
			if method == "ANY" {
				classification = "unresolved"
			} else if method != "GET" && method != "HEAD" && method != "OPTIONS" {
				classification = "mutation"
			}
			if method == "DELETE" || strings.Contains(pathValue, "reset") || strings.Contains(pathValue, "revoke") || strings.Contains(pathValue, "unpair") {
				classification = "destructive"
			}
			automation := "missing"
			if strings.Contains(pathValue, "/callback") || strings.Contains(pathValue, "/oauth/") || pathValue == "/api/analytics/event" {
				automation = "browser-or-protocol-exception"
			}
			rows = append(rows, endpoint{Method: method, Path: pathValue, Sources: keys(f.sources), WebsiteConsumer: f.website, CLI: false, Classification: classification, Automation: automation})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Path == rows[j].Path {
			return rows[i].Method < rows[j].Method
		}
		return rows[i].Path < rows[j].Path
	})
	return report{
		SchemaVersion: 1,
		Project:       p.Name,
		Repository:    p.Path,
		Evidence:      keys(evidence),
		CLICommands:   keys(commands),
		Endpoints:     rows,
		Notes: []string{
			"ANY means the current registration or handler does not declare one immutable method in a scanner-readable form.",
			"CLI=false is the conservative Phase 1 baseline; CP8 registers proven shared operations and Phase 5 closes all functional gaps.",
			"Browser/protocol exceptions remain HTTP operations but do not require artificial CLI wrappers.",
		},
	}, nil
}

func add(all map[string]*found, pathValue, method, source string, website bool) {
	f := all[pathValue]
	if f == nil {
		f = &found{methods: map[string]bool{}, sources: map[string]bool{}}
		all[pathValue] = f
	}
	f.methods[method] = true
	f.sources[source] = true
	f.website = f.website || website
}

func keys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "surface-inventory:", err)
	os.Exit(1)
}
