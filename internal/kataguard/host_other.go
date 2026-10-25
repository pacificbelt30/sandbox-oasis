//go:build !linux

package kataguard

import (
	"context"
	"fmt"
	"os"
)

type Config struct {
	Manifest, CgroupRoot, StateDir, Endpoint, Socket string
	ResourceProfiles                                 string
	MaxWorkers                                       int
	DNS                                              []string
}
type Guard struct {
	config           Config
	manifest         Manifest
	profiles         []ResourceProfile
	temporaryStorage bool
}

func trustedOwner(info os.FileInfo) bool { return false }
func rootOwner(info os.FileInfo) bool    { return false }
func New(Config) (*Guard, error) {
	return nil, fmt.Errorf("Kata node verification requires Linux; use explicit runc compatibility on this host")
}
func (*Guard) Validate(context.Context, string) error { return fmt.Errorf("Kata requires Linux") }
func (*Guard) Reserve(context.Context, string) (string, error) {
	return "", fmt.Errorf("Kata requires Linux")
}
func (*Guard) ReserveProfile(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("Kata requires Linux")
}
func (*Guard) Verify(context.Context, string, string) error { return fmt.Errorf("Kata requires Linux") }
func (*Guard) Retire(context.Context, string, string) error { return fmt.Errorf("Kata requires Linux") }
func (*Guard) ConfirmStopped(context.Context, string, string) error {
	return fmt.Errorf("Kata requires Linux")
}
func (*Guard) AbortCreate(context.Context, string, string) error {
	return fmt.Errorf("Kata requires Linux")
}
func (*Guard) Release(context.Context, string, string) error {
	return fmt.Errorf("Kata requires Linux")
}
func (*Guard) ProtectedPaths() []string      { return nil }
func (*Guard) Launcher() string              { return "" }
func (*Guard) Parent(string) (string, error) { return "", fmt.Errorf("Kata requires Linux") }
func (*Guard) Stamp() string                 { return "" }

func (*Guard) Inventory(context.Context) (Inventory, error) {
	return Inventory{}, fmt.Errorf("Kata requires Linux")
}
func (*Guard) RecoverOrphan(context.Context, string, bool) error {
	return fmt.Errorf("Kata requires Linux")
}

func (*Guard) VerifyRuntime(context.Context, string, string, string) error {
	return fmt.Errorf("Kata requires Linux")
}
