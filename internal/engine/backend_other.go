//go:build !linux

package engine

import (
	"context"
	"fmt"
	"net/http"
)

type Options struct {
	Address, State, Runtime, KataConfig, Snapshotter string
	Logger                                           string
	DNS                                              []string
	TemporaryStorage                                 bool
	Retire                                           func(context.Context, string, string) error
	AbortCreate                                      func(context.Context, string, string) error
	ConfirmStopped                                   func(context.Context, string, string) error
	Tools                                            map[string]string
}
type Backend struct{}

func (*Backend) FindRequest(context.Context, string) (string, string, string, bool, error) {
	return "", "", "", false, fmt.Errorf("node requests require Linux")
}

func Open(Options) (*Backend, error) {
	return nil, fmt.Errorf("the containerd/Dragonball node requires Linux")
}
func (*Backend) Close() error { return nil }
func (*Backend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "requires Linux", 503)
}
