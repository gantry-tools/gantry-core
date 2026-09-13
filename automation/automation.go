// Package automation executes declared Gantry operation contracts over HTTP
// using the common CLI grammar. It deliberately supports protected token files
// and protected session files rather than raw credential arguments.
package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/gantry-tools/gantry-core/cli"
	"github.com/gantry-tools/gantry-core/operation"
)

const maxBody = 1 << 20

var pathVar = regexp.MustCompile(`\{[^{}]+\}`)

type Options struct {
	Program         string
	DefaultURL      string
	CookieName      string
	CSRFHeader      string
	CSRFFields      []string
	SessionInfoPath string
	HTTPClient      *http.Client
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
}

type sessionFile struct {
	Cookie string `json:"cookie"`
	CSRF   string `json:"csrf,omitempty"`
}

func Run(args []string, contracts []operation.Contract, options Options) int {
	inv, err := cli.Parse(args)
	if err != nil {
		if errors.Is(err, cli.ErrHelp) {
			fmt.Fprintln(output(options.Stderr, os.Stderr), cli.Usage(options.Program))
			return cli.ExitOK
		}
		fmt.Fprintln(output(options.Stderr, os.Stderr), options.Program+":", err)
		return cli.ExitUsage
	}
	c, ok := find(contracts, inv.Resource, inv.Verb)
	if !ok || c.CLI == nil || !c.CLI.Implemented {
		fmt.Fprintf(output(options.Stderr, os.Stderr), "%s: unsupported operation %s %s\n", options.Program, inv.Resource, inv.Verb)
		return cli.ExitUsage
	}
	if c.Kind == operation.Destructive && !inv.Confirm {
		fmt.Fprintf(output(options.Stderr, os.Stderr), "%s: destructive operation requires --yes\n", options.Program)
		return cli.ExitUsage
	}
	base := inv.URL
	if base == "" {
		base = options.DefaultURL
	}
	if base == "" {
		fmt.Fprintln(output(options.Stderr, os.Stderr), options.Program+": --url is required")
		return cli.ExitUsage
	}
	endpoint, err := routeURL(base, c.Route.Path, inv.Arguments, inv.Query)
	if err != nil {
		fmt.Fprintln(output(options.Stderr, os.Stderr), options.Program+":", err)
		return cli.ExitUsage
	}
	body, err := readInput(inv.Input, input(options.Stdin, os.Stdin))
	if err != nil {
		fmt.Fprintln(output(options.Stderr, os.Stderr), options.Program+":", err)
		return cli.ExitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), inv.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, c.Route.Method, endpoint, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(output(options.Stderr, os.Stderr), options.Program+":", err)
		return cli.ExitFailure
	}
	req.Header.Set("Accept", "application/json")
	if len(body) != 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if inv.RequestID != "" {
		req.Header.Set("X-Request-ID", inv.RequestID)
		if c.Idempotency.Supported {
			req.Header.Set("Idempotency-Key", inv.RequestID)
		}
	}
	if inv.TokenFile != "" {
		token, e := readSecret(inv.TokenFile)
		if e != nil {
			fmt.Fprintln(output(options.Stderr, os.Stderr), options.Program+":", e)
			return cli.ExitAuth
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	var loaded sessionFile
	if inv.SessionFile != "" {
		if data, e := readProtectedFile(inv.SessionFile); e == nil {
			if json.Unmarshal(data, &loaded) != nil || loaded.Cookie == "" {
				fmt.Fprintln(output(options.Stderr, os.Stderr), options.Program+": invalid session file")
				return cli.ExitAuth
			}
			if options.CookieName == "" {
				fmt.Fprintln(output(options.Stderr, os.Stderr), options.Program+": session authentication unsupported")
				return cli.ExitAuth
			}
			req.AddCookie(&http.Cookie{Name: options.CookieName, Value: loaded.Cookie})
			if loaded.CSRF != "" && options.CSRFHeader != "" && c.Kind != operation.Read {
				req.Header.Set(options.CSRFHeader, loaded.CSRF)
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			fmt.Fprintln(output(options.Stderr, os.Stderr), options.Program+":", e)
			return cli.ExitAuth
		} else if c.Authorization.Boundary != operation.Public {
			fmt.Fprintln(output(options.Stderr, os.Stderr), options.Program+": session file does not exist")
			return cli.ExitAuth
		}
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: inv.Timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(output(options.Stderr, os.Stderr), options.Program+":", err)
		return cli.ExitUnavailable
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		fmt.Fprintln(output(options.Stderr, os.Stderr), options.Program+": response too large or unreadable")
		return cli.ExitFailure
	}
	if inv.SessionFile != "" && options.CookieName != "" {
		for _, ck := range resp.Cookies() {
			if ck.Name == options.CookieName && ck.Value != "" {
				csrf := findCSRF(data, options.CSRFFields)
				if csrf == "" && options.SessionInfoPath != "" {
					csrf = fetchCSRF(ctx, client, base, options, ck.Value)
				}
				if csrf == "" {
					csrf = loaded.CSRF
				}
				if e := writeSession(inv.SessionFile, sessionFile{Cookie: ck.Value, CSRF: csrf}); e != nil {
					fmt.Fprintln(output(options.Stderr, os.Stderr), options.Program+":", e)
					return cli.ExitFailure
				}
			}
		}
	}
	if !inv.Quiet && len(data) != 0 {
		out := output(options.Stdout, os.Stdout)
		if inv.Output == cli.JSON {
			_, _ = out.Write(append(bytes.TrimSpace(data), '\n'))
		} else {
			var pretty bytes.Buffer
			if json.Indent(&pretty, data, "", "  ") == nil {
				_, _ = out.Write(append(pretty.Bytes(), '\n'))
			} else {
				_, _ = out.Write(data)
				if len(data) == 0 || data[len(data)-1] != '\n' {
					fmt.Fprintln(out)
				}
			}
		}
	}
	return exitForStatus(resp.StatusCode)
}

func find(contracts []operation.Contract, resource, verb string) (operation.Contract, bool) {
	for _, c := range contracts {
		if c.CLI != nil && c.CLI.Resource == resource && c.CLI.Verb == verb {
			return c, true
		}
	}
	return operation.Contract{}, false
}
func routeURL(base, path string, args []string, queries []string) (string, error) {
	vars := pathVar.FindAllString(path, -1)
	if len(args) != len(vars) {
		return "", fmt.Errorf("operation requires %d path arguments, got %d", len(vars), len(args))
	}
	for i, v := range vars {
		path = strings.Replace(path, v, url.PathEscape(args[i]), 1)
	}
	u, err := url.Parse(strings.TrimRight(base, "/") + path)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("invalid operation URL")
	}
	q := u.Query()
	for _, item := range queries {
		k, v, ok := strings.Cut(item, "=")
		if !ok || k == "" {
			return "", errors.New("query parameters must be key=value")
		}
		q.Add(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
func readInput(path string, stdin io.Reader) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	var r io.Reader
	if path == "-" {
		r = stdin
	} else {
		f, e := os.Open(path)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		r = f
	}
	data, e := io.ReadAll(io.LimitReader(r, maxBody+1))
	if e != nil {
		return nil, e
	}
	if len(data) > maxBody {
		return nil, errors.New("input exceeds 1 MiB")
	}
	if !json.Valid(data) {
		return nil, errors.New("input must be valid JSON")
	}
	return data, nil
}
func readSecret(path string) (string, error) {
	data, e := readProtectedFile(path)
	if e != nil {
		return "", e
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return "", errors.New("credential file is empty")
	}
	return v, nil
}
func readProtectedFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("credential file %s must not be accessible by group/others (use chmod 600)", path)
	}
	return os.ReadFile(path)
}

func writeSession(path string, s sessionFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil && filepath.Dir(path) != "." {
		return err
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	data = append(data, '\n')
	return os.WriteFile(path, data, 0600)
}

func fetchCSRF(ctx context.Context, client *http.Client, base string, options Options, cookie string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+options.SessionInfoPath, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: options.CookieName, Value: cookie})
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return ""
	}
	return findCSRF(data, options.CSRFFields)
}

func findCSRF(data []byte, fields []string) string {
	var v map[string]any
	if json.Unmarshal(data, &v) != nil {
		return ""
	}
	for _, f := range fields {
		if x, ok := v[f].(string); ok && x != "" {
			return x
		}
	}
	return ""
}
func exitForStatus(status int) int {
	switch {
	case status >= 200 && status < 300:
		return cli.ExitOK
	case status == 401 || status == 403:
		return cli.ExitAuth
	case status == 404:
		return cli.ExitNotFound
	case status == 409:
		return cli.ExitConflict
	case status == 502 || status == 503 || status == 504:
		return cli.ExitUnavailable
	default:
		return cli.ExitFailure
	}
}
func output(got io.Writer, fallback io.Writer) io.Writer {
	if got == nil {
		return fallback
	}
	return got
}

func input(got io.Reader, fallback io.Reader) io.Reader {
	if got == nil {
		return fallback
	}
	return got
}
