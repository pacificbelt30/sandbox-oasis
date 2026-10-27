package engine

import (
	"context"
	"slices"
	"testing"

	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/opencontainers/runtime-spec/specs-go"
)

func TestWorkerTTYRetainsDeclaredTerminalCapabilities(t *testing.T) {
	for _, test := range []struct {
		name string
		env  []string
		want []string
	}{
		{"256 colors", []string{"TERM=xterm-256color", "LANG=C.UTF-8"}, []string{"TERM=xterm-256color", "LANG=C.UTF-8"}},
		{"explicit dumb terminal", []string{"TERM=dumb"}, []string{"TERM=dumb"}},
		{"unspecified", []string{"LANG=C.UTF-8"}, []string{"LANG=C.UTF-8", "TERM=xterm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := &oci.Spec{Linux: &specs.Linux{}, Process: &specs.Process{Env: slices.Clone(test.env)}}
			if err := withWorkerTTY(context.Background(), nil, nil, spec); err != nil {
				t.Fatal(err)
			}
			if !spec.Process.Terminal || !slices.Equal(spec.Process.Env, test.want) {
				t.Fatalf("TTY capabilities changed: terminal=%t env=%q; want %q", spec.Process.Terminal, spec.Process.Env, test.want)
			}
		})
	}
}
