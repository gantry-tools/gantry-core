// Package contracttest validates a product's declared operation surface against
// the HTTP routes and website operations observed by that product's own tests.
package contracttest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gantry-tools/gantry-core/operation"
)

type Exception struct {
	Operation string `json:"operation"`
	Reason    string `json:"reason"`
}

type Manifest struct {
	SchemaVersion     int                  `json:"schema_version"`
	Project           string               `json:"project"`
	Operations        []operation.Contract `json:"operations"`
	ObservedRoutes    []operation.Route    `json:"observed_routes"`
	WebsiteOperations []string             `json:"website_operations"`
	Exceptions        []Exception          `json:"exceptions,omitempty"`
}

type Finding struct {
	Code    string `json:"code"`
	Subject string `json:"subject"`
	Message string `json:"message"`
}

type Report struct {
	Project  string    `json:"project"`
	Findings []Finding `json:"findings"`
}

func (r Report) OK() bool { return len(r.Findings) == 0 }

func Check(manifest Manifest) Report {
	report := Report{Project: manifest.Project}
	add := func(code, subject, message string) {
		report.Findings = append(report.Findings, Finding{Code: code, Subject: subject, Message: message})
	}
	if manifest.SchemaVersion != 1 {
		add("manifest.version", manifest.Project, "unsupported manifest schema version")
	}
	if strings.TrimSpace(manifest.Project) == "" {
		add("manifest.project", "", "project is required")
	}
	registry, err := operation.NewRegistry(manifest.Operations...)
	if err != nil {
		add("operations.invalid", manifest.Project, err.Error())
	}
	declaredIDs := map[string]bool{}
	declaredRoutes := map[string]string{}
	for _, contract := range manifest.Operations {
		declaredIDs[contract.ID] = true
		declaredRoutes[routeKey(contract.Route)] = contract.ID
	}
	exceptions := map[string]bool{}
	for _, exception := range manifest.Exceptions {
		if exception.Operation == "" || strings.TrimSpace(exception.Reason) == "" {
			add("exception.invalid", exception.Operation, "exception requires an operation and reason")
			continue
		}
		if exceptions[exception.Operation] {
			add("exception.duplicate", exception.Operation, "exception is declared more than once")
		}
		exceptions[exception.Operation] = true
	}
	for _, route := range manifest.ObservedRoutes {
		key := routeKey(route)
		if _, exists := declaredRoutes[key]; !exists {
			add("route.undeclared", key, "observed HTTP route has no operation contract")
		}
	}
	observedRoutes := map[string]bool{}
	for _, route := range manifest.ObservedRoutes {
		observedRoutes[routeKey(route)] = true
	}
	for key, id := range declaredRoutes {
		if !observedRoutes[key] {
			add("route.unobserved", id, "declared operation route is not observed by the product")
		}
	}
	for _, id := range manifest.WebsiteOperations {
		if !declaredIDs[id] && !exceptions[id] {
			add("website.undeclared", id, "website operation has no contract or documented exception")
		}
	}
	for id := range exceptions {
		if declaredIDs[id] {
			add("exception.stale", id, "exception refers to a declared operation")
		}
	}
	if registry != nil {
		for _, contract := range registry.Contracts() {
			if contract.Automation == operation.Automatable && contract.CLI == nil {
				add("cli.missing", contract.ID, "automatable operation has no CLI mapping")
			}
			if contract.Kind != operation.Read && !contract.Audit.Required {
				add("audit.missing", contract.ID, "mutation has no required audit event")
			}
		}
	}
	sort.Slice(report.Findings, func(i, j int) bool {
		left := report.Findings[i].Code + "\x00" + report.Findings[i].Subject
		right := report.Findings[j].Code + "\x00" + report.Findings[j].Subject
		return left < right
	})
	return report
}

func Require(manifest Manifest) error {
	report := Check(manifest)
	if report.OK() {
		return nil
	}
	parts := make([]string, 0, len(report.Findings))
	for _, finding := range report.Findings {
		parts = append(parts, fmt.Sprintf("%s %s: %s", finding.Code, finding.Subject, finding.Message))
	}
	return fmt.Errorf("%s operation contract failed:\n%s", manifest.Project, strings.Join(parts, "\n"))
}

func routeKey(route operation.Route) string { return route.Method + " " + route.Path }
