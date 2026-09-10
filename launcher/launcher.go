// Package launcher defines the portable Gantry instance-catalogue contract.
package launcher

import (
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	coreauth "github.com/gantry-tools/gantry-core/auth"
)

var accessErrorPage = template.Must(template.New("launcher-access-error").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="dark"><title>{{.Heading}} · {{.Product}}</title>
<style>:root{color-scheme:dark;font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#151817;color:#edf1ee;--accent:{{.Accent}};--sorbet-red:#f38f92}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px}.card{width:min(460px,100%);background:#1d211f;border:1px solid #3e3838;border-radius:12px;padding:30px;box-shadow:0 18px 50px rgba(0,0,0,.24)}.brand{display:flex;align-items:center;gap:12px;margin-bottom:26px}.mark{display:grid;place-items:center;width:34px;height:34px;border:1px solid color-mix(in srgb,var(--accent) 60%,#3b433e);border-radius:7px;color:var(--accent);font-weight:800}.brand strong{font-size:17px}.code{color:var(--sorbet-red);font-size:12px;font-weight:750;letter-spacing:.12em;text-transform:uppercase}h1{margin:8px 0 10px;color:var(--sorbet-red);font-size:28px;line-height:1.15}p{margin:0;color:#aab3ad;line-height:1.55}.actions{display:flex;gap:10px;margin-top:26px;flex-wrap:wrap}a{display:inline-flex;align-items:center;justify-content:center;min-height:40px;padding:0 15px;border-radius:7px;border:1px solid #3b433e;color:#e5ebe7;text-decoration:none;font-weight:650}a.primary{background:var(--accent);color:#101310;border-color:var(--accent)}a.primary:hover{filter:brightness(1.08)}a:not(.primary):hover{border-color:color-mix(in srgb,var(--accent) 55%,#3b433e);color:var(--accent)}</style></head>
<body><main class="card"><div class="brand"><span class="mark">{{.Mark}}</span><strong>{{.Product}}</strong></div><div class="code">HTTP {{.Status}}</div><h1>{{.Heading}}</h1><p>{{.Message}}</p><div class="actions"><a class="primary" href="{{.ActionURL}}">{{.ActionLabel}}</a><a href="/">Back to launcher</a></div></main></body></html>`))

// WriteAccessError renders the shared Gantry launcher authentication and
// authorization error page. Launcher configuration APIs remain the security
// boundary; this page makes a denied browser navigation explicit and useful.
func WriteAccessError(w http.ResponseWriter, status int, product, mark, accent string) {
	heading, message, actionLabel := "Permission denied", "Your account does not have permission to configure this launcher.", "Open application"
	if status == http.StatusUnauthorized {
		heading, message, actionLabel = "Authentication required", "Sign in with an account that can configure this launcher, then try again.", "Sign in"
	} else {
		status = http.StatusForbidden
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(accent) {
		accent = "#7fc89b"
	}
	_ = accessErrorPage.Execute(w, map[string]any{"Status": status, "Product": product, "Mark": mark, "Accent": template.CSS(accent), "Heading": heading, "Message": message, "ActionLabel": actionLabel, "ActionURL": "/app/?return=%2F%3Fconfig"})
}

const Version = 1

var idPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

type Instance struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name"`
	Domain string `json:"domain"`
	Port   *int   `json:"port,omitempty"`
}

type InstanceView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Domain string `json:"domain"`
	Port   *int   `json:"port,omitempty"`
	AppURL string `json:"appUrl"`
}

type Document struct {
	Version   int        `json:"version"`
	Product   string     `json:"product,omitempty"`
	Instances []Instance `json:"instances"`
}

type View struct {
	Version   int            `json:"version"`
	Product   string         `json:"product"`
	Instances []InstanceView `json:"instances"`
}

func Normalize(product string, document Document) ([]Instance, error) {
	if document.Version != Version {
		return nil, errors.New("unsupported launcher configuration version")
	}
	if document.Product != "" && document.Product != product {
		return nil, errors.New("launcher configuration is for another product")
	}
	if document.Instances == nil {
		return nil, errors.New("instances must be an array")
	}
	if len(document.Instances) > 1000 {
		return nil, errors.New("too many launcher instances")
	}
	seen := make(map[string]bool, len(document.Instances))
	out := make([]Instance, 0, len(document.Instances))
	for _, item := range document.Instances {
		item.Name = strings.TrimSpace(item.Name)
		if item.Name == "" || len(item.Name) > 100 {
			return nil, errors.New("every instance requires a name of at most 100 characters")
		}
		domain, err := normalizeDomain(item.Domain)
		if err != nil {
			return nil, err
		}
		item.Domain = domain
		if item.Port != nil && (*item.Port < 1 || *item.Port > 65535) {
			return nil, errors.New("instance ports must be from 1 to 65535")
		}
		item.ID = strings.TrimSpace(item.ID)
		if !idPattern.MatchString(item.ID) || seen[item.ID] {
			item.ID = coreauth.NewID("instance")
			for seen[item.ID] {
				item.ID = coreauth.NewID("instance")
			}
		}
		seen[item.ID] = true
		out = append(out, item)
	}
	return out, nil
}

func normalizeDomain(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 512 || strings.IndexFunc(raw, func(r rune) bool { return r <= ' ' }) >= 0 {
		return "", errors.New("every instance requires a valid domain or IP")
	}
	probe := raw
	if !strings.Contains(probe, "://") {
		probe = "https://" + probe
	}
	u, err := url.Parse(probe)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("every instance requires a valid HTTP(S) domain or IP without a path")
	}
	return strings.TrimSuffix(raw, "/"), nil
}

func AppURL(instance Instance) (string, error) {
	raw := instance.Domain
	if !strings.Contains(raw, "://") {
		probe, err := url.Parse("https://" + raw)
		if err != nil {
			return "", err
		}
		scheme := "https"
		host := probe.Hostname()
		if host == "localhost" || strings.HasSuffix(host, ".localhost") || net.ParseIP(host) != nil {
			scheme = "http"
		}
		raw = scheme + "://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if instance.Port != nil {
		u.Host = net.JoinHostPort(u.Hostname(), fmt.Sprint(*instance.Port))
	}
	u.Path, u.RawPath, u.RawQuery, u.Fragment = "/app/", "", "", ""
	return u.String(), nil
}

func MakeView(product string, instances []Instance) (View, error) {
	view := View{Version: Version, Product: product, Instances: make([]InstanceView, 0, len(instances))}
	for _, item := range instances {
		appURL, err := AppURL(item)
		if err != nil {
			return View{}, err
		}
		view.Instances = append(view.Instances, InstanceView{ID: item.ID, Name: item.Name, Domain: item.Domain, Port: item.Port, AppURL: appURL})
	}
	return view, nil
}
