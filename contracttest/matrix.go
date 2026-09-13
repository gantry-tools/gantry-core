package contracttest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gantry-tools/gantry-core/operation"
)

// Evidence records where an operation is exercised independently of its
// declaration. Tests should name stable package/test/script identifiers rather
// than prose claims so generated coverage remains reviewable.
type Evidence struct {
	Website bool     `json:"website"`
	Tests   []string `json:"tests,omitempty"`
}

// MatrixRow is the generated, human- and machine-readable coverage statement
// for one functional operation.
type MatrixRow struct {
	Operation    string   `json:"operation"`
	Website      bool     `json:"website"`
	Method       string   `json:"method"`
	Route        string   `json:"route"`
	CLI          string   `json:"cli,omitempty"`
	CLIObserved  bool     `json:"cli_observed"`
	Boundary     string   `json:"boundary"`
	Permission   string   `json:"permission,omitempty"`
	TokenScopes  []string `json:"token_scopes,omitempty"`
	InputSchema  string   `json:"input_schema,omitempty"`
	OutputSchema string   `json:"output_schema,omitempty"`
	Tests        []string `json:"tests,omitempty"`
}

type Matrix struct {
	SchemaVersion int         `json:"schema_version"`
	Project       string      `json:"project"`
	Operations    []MatrixRow `json:"operations"`
}

func BuildMatrix(manifest Manifest) Matrix {
	observed := map[string]bool{}
	for _, cmd := range manifest.ObservedCommands {
		observed[cmd.Resource+" "+cmd.Verb] = true
	}
	website := map[string]bool{}
	for _, id := range manifest.WebsiteOperations {
		website[id] = true
	}
	for id, evidence := range manifest.Evidence {
		if evidence.Website {
			website[id] = true
		}
	}
	rows := make([]MatrixRow, 0, len(manifest.Operations))
	for _, c := range manifest.Operations {
		row := MatrixRow{
			Operation:    c.ID,
			Website:      website[c.ID],
			Method:       c.Route.Method,
			Route:        c.Route.Path,
			Boundary:     string(c.Authorization.Boundary),
			Permission:   c.Authorization.Capability,
			TokenScopes:  append([]string(nil), c.Authorization.TokenScopes...),
			InputSchema:  c.Schemas.Input,
			OutputSchema: c.Schemas.Output,
		}
		if c.CLI != nil {
			row.CLI = c.CLI.Resource + " " + c.CLI.Verb
			row.CLIObserved = observed[row.CLI]
		}
		if ev, ok := manifest.Evidence[c.ID]; ok {
			row.Tests = append([]string(nil), ev.Tests...)
		}
		sort.Strings(row.TokenScopes)
		sort.Strings(row.Tests)
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Operation < rows[j].Operation })
	return Matrix{SchemaVersion: 1, Project: manifest.Project, Operations: rows}
}

func (m Matrix) JSON() ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

func (m Matrix) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s functional coverage\n\n", m.Project)
	b.WriteString("Generated from the tested operation manifest. Do not edit by hand.\n\n")
	b.WriteString("| Operation | Website | API | CLI | Permission | Schemas | Tests |\n")
	b.WriteString("| --- | :---: | --- | --- | --- | --- | --- |\n")
	for _, r := range m.Operations {
		cli := r.CLI
		if cli == "" {
			cli = "—"
		} else if !r.CLIObserved {
			cli += " (unobserved)"
		}
		permission := r.Boundary
		if r.Permission != "" {
			permission += ":" + r.Permission
		}
		if len(r.TokenScopes) != 0 {
			permission += " token=" + strings.Join(r.TokenScopes, ",")
		}
		schemas := "—"
		if r.InputSchema != "" || r.OutputSchema != "" {
			schemas = emptyDash(r.InputSchema) + " → " + emptyDash(r.OutputSchema)
		}
		tests := "—"
		if len(r.Tests) != 0 {
			tests = strings.Join(r.Tests, "<br>")
		}
		fmt.Fprintf(&b, "| `%s` | %s | `%s %s` | `%s` | `%s` | `%s` | %s |\n",
			r.Operation, yesNo(r.Website), r.Method, r.Route, cli, permission, schemas, tests)
	}
	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
func emptyDash(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

// CertificationFindings returns stricter Phase-5 findings than Check. It
// requires every declared operation to have independent test evidence and every
// automatable operation to expose an observed CLI command and schema names.
func CertificationFindings(manifest Manifest) []Finding {
	findings := append([]Finding(nil), Check(manifest).Findings...)
	observed := map[string]bool{}
	for _, c := range manifest.ObservedCommands {
		observed[c.Resource+" "+c.Verb] = true
	}
	for _, c := range manifest.Operations {
		ev := manifest.Evidence[c.ID]
		if len(ev.Tests) == 0 {
			findings = append(findings, Finding{Code: "evidence.missing", Subject: c.ID, Message: "operation has no independent test evidence"})
		}
		if c.Automation == operation.Automatable {
			if c.CLI == nil || !c.CLI.Implemented || !observed[c.CLI.Resource+" "+c.CLI.Verb] {
				findings = append(findings, Finding{Code: "cli.uncertified", Subject: c.ID, Message: "automatable operation has no observed implemented CLI command"})
			}
			if c.Schemas.Output == "" || (c.Kind != operation.Read && c.Schemas.Input == "") {
				findings = append(findings, Finding{Code: "schema.missing", Subject: c.ID, Message: "automatable operation lacks named input/output schema coverage"})
			}
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		a := findings[i].Code + "\x00" + findings[i].Subject
		b := findings[j].Code + "\x00" + findings[j].Subject
		return a < b
	})
	return findings
}

// CheckMatrixArtifacts verifies committed generated coverage artifacts match the
// current tested manifest byte-for-byte. CI can call this from product tests so
// coverage documentation cannot silently drift from runtime contracts.
func CheckMatrixArtifacts(jsonPath, markdownPath string, manifest Manifest) error {
	matrix := BuildMatrix(manifest)
	wantJSON, err := matrix.JSON()
	if err != nil {
		return err
	}
	wantJSON = append(wantJSON, '\n')
	gotJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("read coverage JSON: %w", err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		return fmt.Errorf("coverage JSON is stale: regenerate %s", jsonPath)
	}
	wantMD := []byte(matrix.Markdown())
	gotMD, err := os.ReadFile(markdownPath)
	if err != nil {
		return fmt.Errorf("read coverage Markdown: %w", err)
	}
	if !bytes.Equal(gotMD, wantMD) {
		return fmt.Errorf("coverage Markdown is stale: regenerate %s", markdownPath)
	}
	return nil
}

// WriteMatrixArtifacts writes deterministic machine- and human-readable
// coverage snapshots. The files are intended to be committed and checked by CI.
func WriteMatrixArtifacts(jsonPath, markdownPath string, manifest Manifest) error {
	matrix := BuildMatrix(manifest)
	jsonData, err := matrix.JSON()
	if err != nil {
		return err
	}
	jsonData = append(jsonData, '\n')
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(markdownPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, jsonData, 0o644); err != nil {
		return err
	}
	return os.WriteFile(markdownPath, []byte(matrix.Markdown()), 0o644)
}

// SecurityFindings applies the Phase-5 authorization/secret/audit rules that
// are independent of any one product implementation. Product tests remain
// responsible for exercising disabled/revoked actors and application policy.
func SecurityFindings(manifest Manifest) []Finding {
	var findings []Finding
	for _, c := range manifest.Operations {
		if c.Authorization.Boundary == operation.Public && c.Kind == operation.Destructive {
			findings = append(findings, Finding{Code: "security.public-destructive", Subject: c.ID, Message: "destructive operation cannot be public"})
		}
		if len(c.Authorization.TokenScopes) != 0 && c.Authorization.Boundary != operation.Session && c.Authorization.Boundary != operation.Capability {
			findings = append(findings, Finding{Code: "security.token-boundary", Subject: c.ID, Message: "API-token scopes require a human session/capability boundary"})
		}
		if c.Kind != operation.Read && (!c.Audit.Required || c.Audit.Event == "") {
			findings = append(findings, Finding{Code: "security.audit", Subject: c.ID, Message: "mutation/destructive operation lacks an audit event"})
		}
		if c.Kind == operation.Read && len(c.SecretInputs) != 0 {
			findings = append(findings, Finding{Code: "security.read-secret-input", Subject: c.ID, Message: "read operation declares secret input"})
		}
		if c.Automation == operation.Automatable && c.Kind == operation.Destructive && (c.CLI == nil || !c.CLI.Implemented) {
			findings = append(findings, Finding{Code: "security.destructive-cli", Subject: c.ID, Message: "destructive operation lacks confirmed CLI path"})
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		a := findings[i].Code + "\x00" + findings[i].Subject
		b := findings[j].Code + "\x00" + findings[j].Subject
		return a < b
	})
	return findings
}
