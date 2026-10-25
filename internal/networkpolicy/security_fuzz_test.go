package networkpolicy

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"testing"
)

type fuzzResolver struct{}

func (fuzzResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("203.0.113.1"), netip.MustParseAddr("169.254.169.254")}, nil
}
func FuzzDestinationCannotBypassPrivateDeny(f *testing.F) {
	for _, seed := range []string{"allowed.example:443", "127.0.0.1:443", "[::ffff:169.254.169.254]:443", "allowed.example%00.evil:443", "allowed.example:443@127.0.0.1:443", "[fd00:ec2::254]:443"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, authority string) {
		if len(authority) > 4096 {
			t.Skip()
		}
		p, err := Compile(Rules{DefaultDeny: true, BlockPrivate: true, AllowDomains: []string{"=allowed.example"}})
		if err != nil {
			t.Fatal(err)
		}
		dialer := Dialer{Policy: p, Resolver: fuzzResolver{}, Connect: func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("private/mixed answer reached connector")
			return nil, fmt.Errorf("fixture")
		}}
		conn, err := dialer.DialContext(context.Background(), "tcp", authority)
		if conn != nil {
			_ = conn.Close()
			t.Fatal("forbidden connection created")
		}
		if err == nil {
			t.Fatal("destination bypassed default/private deny")
		}
	})
}
