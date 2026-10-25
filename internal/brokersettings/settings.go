// Package brokersettings edits trusted broker routes without rendering secrets.
package brokersettings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/internal/privatefile"
)

type Descriptor struct {
	Name      string   `json:"name"`
	Upstream  string   `json:"upstream"`
	Paths     []string `json:"paths"`
	Methods   []string `json:"methods"`
	Header    string   `json:"header"`
	Prefix    string   `json:"prefix"`
	Provider  string   `json:"provider,omitempty"`
	SecretSet bool     `json:"secret_set"`
}

type storedRoute struct {
	Name, Upstream, Header, Prefix, Provider, Secret string
	Paths, Methods                                   []string
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "codex-dock", "broker", "routes.json"), nil
}

// Template scopes are intentionally read-only; every origin/path is explicit.
func Template(name, route, origin, scope string) (Descriptor, error) {
	d := Descriptor{Name: route, Upstream: origin, Paths: []string{scope}, Methods: []string{"GET", "HEAD"}, Header: "Authorization", Prefix: "Bearer "}
	switch name {
	case "bearer-readonly":
	case "api-key-readonly":
		d.Header, d.Prefix = "X-Api-Key", ""
	default:
		return Descriptor{}, fmt.Errorf("unknown broker template")
	}
	return d, Validate(d)
}

func Validate(d Descriptor) error {
	return authproxy.ValidateCredentialRoutes([]authproxy.CredentialRoute{{Name: d.Name, Upstream: d.Upstream, Paths: d.Paths, Methods: d.Methods, Header: d.Header, Prefix: d.Prefix, Provider: d.Provider, Secret: func(context.Context) (string, error) { return "", nil }}})
}

func decode(data []byte) ([]storedRoute, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var routes []storedRoute
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&routes); err != nil {
		return nil, fmt.Errorf("invalid broker configuration")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("invalid broker configuration")
	}
	if len(routes) > 128 {
		return nil, fmt.Errorf("too many broker routes (maximum 128)")
	}
	seen := map[string]bool{}
	for _, r := range routes {
		if err := Validate(describe(r)); err != nil {
			return nil, err
		}
		if r.Secret == "" || strings.ContainsAny(r.Secret, "\r\n") || seen[r.Name] {
			return nil, fmt.Errorf("missing/invalid secret or duplicate broker route")
		}
		seen[r.Name] = true
	}
	return routes, nil
}

func describe(r storedRoute) Descriptor {
	return Descriptor{Name: r.Name, Upstream: r.Upstream, Paths: r.Paths, Methods: r.Methods, Header: r.Header, Prefix: r.Prefix, Provider: r.Provider, SecretSet: r.Secret != ""}
}

// Load returns scope metadata only. Decoder errors never include stored values.
func Load(path string) ([]Descriptor, string, error) {
	data, err := privatefile.Read(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, "", err
	}
	routes, err := decode(data)
	if err != nil {
		return nil, "", err
	}
	descriptors := make([]Descriptor, 0, len(routes))
	for _, r := range routes {
		descriptors = append(descriptors, describe(r))
	}
	return descriptors, privatefile.Revision(data), nil
}

// Save changes one route, preserving other secrets. An empty replacement keeps
// the existing secret. New routes always need a secret through private input.
func Save(path, expected string, d Descriptor, secret string) error {
	if err := Validate(d); err != nil {
		return err
	}
	if strings.ContainsAny(secret, "\r\n") || len(secret) > 64<<10 {
		return fmt.Errorf("invalid broker secret")
	}
	return privatefile.Update(path, expected, func(data []byte) ([]byte, error) {
		routes, err := decode(data)
		if err != nil {
			return nil, err
		}
		replacement := storedRoute{Name: d.Name, Upstream: d.Upstream, Header: d.Header, Prefix: d.Prefix, Provider: d.Provider, Paths: d.Paths, Methods: d.Methods, Secret: secret}
		found := false
		for i, r := range routes {
			if r.Name == d.Name {
				if replacement.Secret == "" {
					replacement.Secret = r.Secret
				}
				routes[i] = replacement
				found = true
				break
			}
		}
		if !found {
			routes = append(routes, replacement)
		}
		output, err := json.MarshalIndent(routes, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("broker encoding failed")
		}
		if _, err := decode(output); err != nil {
			return nil, err
		}
		return append(output, '\n'), nil
	})
}
