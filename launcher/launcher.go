// Package launcher defines the portable Gantry instance-catalogue contract.
package launcher

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	coreauth "github.com/gantry-tools/gantry-core/auth"
)

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
