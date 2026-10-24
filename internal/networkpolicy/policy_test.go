package networkpolicy

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
)

func TestRulePrecedenceAndIntersection(t *testing.T) {
	p, err := Compile(Rules{AllowDomains: []string{".Example.COM."}, DenyDomains: []string{"blocked.example.com"},
		AllowCIDRs: []string{"203.0.113.0/24"}, DenyCIDRs: []string{"203.0.113.9"}, AllowPorts: []int{80, 443}, DenyPorts: []int{80}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		authority, ip string
		allowed       bool
	}{
		{"api.example.com:443", "203.0.113.8", true},
		{"blocked.example.com:443", "203.0.113.8", false},
		{"sub.blocked.example.com:443", "203.0.113.8", false},
		{"notexample.com:443", "203.0.113.8", false},
		{"example.com:80", "203.0.113.8", false},
		{"example.com:22", "203.0.113.8", false},
		{"example.com:443", "203.0.113.9", false},
		{"example.com:443", "198.51.100.1", false},
	} {
		t.Run(tc.authority+"/"+tc.ip, func(t *testing.T) {
			dst, err := ParseDestination(tc.authority, "")
			if err != nil {
				t.Fatal(err)
			}
			err = p.CheckDestination(context.Background(), dst)
			if err == nil {
				err = p.CheckAddress(context.Background(), dst, netip.MustParseAddr(tc.ip))
			}
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%t: %v", tc.allowed, err)
			}
		})
	}
}

func TestInvalidPolicyRejected(t *testing.T) {
	for _, rules := range []Rules{
		{AllowDomains: []string{""}}, {DenyDomains: []string{"*.example.com"}}, {AllowDomains: []string{"https://example.com"}},
		{AllowDomains: []string{"example.com:443"}}, {AllowCIDRs: []string{"bad"}}, {DenyCIDRs: []string{"::ffff:0:0/80"}},
		{AllowPorts: []int{0}}, {DenyPorts: []int{65536}},
	} {
		if _, err := Compile(rules); err == nil {
			t.Fatalf("accepted invalid rules: %+v", rules)
		}
	}
}

func TestPrivateIPRulesAndMappedAddresses(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.5.5", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "fd00::1", "fe80::1", "::ffff:127.0.0.1"} {
		if !IsPrivate(netip.MustParseAddr(value)) {
			t.Errorf("allowed private %s", value)
		}
	}
	for _, value := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if IsPrivate(netip.MustParseAddr(value)) {
			t.Errorf("blocked public %s", value)
		}
	}
	p, err := Compile(Rules{DenyCIDRs: []string{"::ffff:192.0.2.0/120"}})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(p.CheckAddress(context.Background(), Destination{Host: "example.com", Port: 443}, netip.MustParseAddr("192.0.2.1")), ErrDenied) {
		t.Fatal("mapped CIDR bypass")
	}
	private, err := Compile(Rules{AllowCIDRs: []string{"127.0.0.0/8"}, BlockPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	if private.CheckAddress(context.Background(), Destination{Host: "localhost", Port: 80}, netip.MustParseAddr("127.0.0.1")) == nil {
		t.Fatal("allowlist overrode private block")
	}
}

func TestDestinationParsing(t *testing.T) {
	for _, tc := range []struct{ authority, port, want string }{
		{"Example.COM.", "443", "example.com:443"}, {"[::1]", "80", "[::1]:80"}, {"[::ffff:192.0.2.1]:443", "", "192.0.2.1:443"},
	} {
		d, err := ParseDestination(tc.authority, tc.port)
		if err != nil || d.String() != tc.want {
			t.Fatalf("%s: %+v %v", tc.authority, d, err)
		}
	}
	for _, authority := range []string{"user@example.com:443", "example.com:0", "example.com:65536", "example.com/path:80", "[fe80::1%eth0]:80", "example.com"} {
		if _, err := ParseDestination(authority, ""); err == nil {
			t.Errorf("accepted %s", authority)
		}
	}
}

type fixedResolver struct {
	addresses []netip.Addr
	calls     int
}

func (r *fixedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.calls++
	return r.addresses, nil
}

func TestDNSAnswersValidatedBeforeAnyDialAndPinned(t *testing.T) {
	policy, err := Compile(Rules{BlockPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	resolver := &fixedResolver{addresses: []netip.Addr{netip.MustParseAddr("203.0.113.1"), netip.MustParseAddr("127.0.0.1")}}
	var dialed []string
	d := Dialer{Policy: policy, Resolver: resolver, Connect: func(_ context.Context, _, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		return nil, errors.New("fixture dial")
	}}
	if _, err := d.DialContext(context.Background(), "tcp", "example.com:443"); !errors.Is(err, ErrDenied) || len(dialed) != 0 {
		t.Fatalf("mixed DNS reached dial: %v %v", dialed, err)
	}
	resolver.addresses = resolver.addresses[:1]
	if _, err := d.DialContext(context.Background(), "tcp", "example.com:443"); err == nil {
		t.Fatal("fixture should fail dial")
	}
	if len(dialed) != 1 || dialed[0] != "203.0.113.1:443" || resolver.calls != 2 {
		t.Fatalf("did not pin validated IP: %v lookups=%d", dialed, resolver.calls)
	}
}

func TestDomainDenialPrecedesDNSAndRulesAreCopied(t *testing.T) {
	rules := Rules{AllowDomains: []string{"example.com"}}
	p, err := Compile(rules)
	if err != nil {
		t.Fatal(err)
	}
	rules.AllowDomains[0] = "evil.com"
	r := &fixedResolver{}
	d := Dialer{Policy: p, Resolver: r}
	if _, err := d.DialContext(context.Background(), "tcp", "evil.com:443"); !errors.Is(err, ErrDenied) || r.calls != 0 {
		t.Fatalf("denial reached DNS: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.CheckDestination(ctx, Destination{Host: "example.com", Port: 443}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	deny, _ := Compile(Rules{DenyDomains: []string{"example.com"}})
	if err := All(p, deny).CheckDestination(context.Background(), Destination{Host: "example.com", Port: 443}); !errors.Is(err, ErrDenied) || !strings.Contains(err.Error(), "example.com") {
		t.Fatalf("extension weakened base: %v", err)
	}
}

func TestMixedDNSFamiliesFailClosedAndPublicAddressesArePinned(t *testing.T) {
	policy, err := Compile(Rules{DefaultDeny: true, AllowDomains: []string{"=fixture.invalid"}, BlockPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, blocked := range []string{"127.0.0.1", "169.254.169.254", "100.100.100.200", "::1", "fd00:ec2::254", "fe80::1", "::ffff:169.254.169.254"} {
		for _, reverse := range []bool{false, true} {
			t.Run(blocked+map[bool]string{false: "/last", true: "/first"}[reverse], func(t *testing.T) {
				ips := []netip.Addr{netip.MustParseAddr("203.0.113.1"), netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr(blocked)}
				if reverse {
					ips[0], ips[2] = ips[2], ips[0]
				}
				resolver := &fixedResolver{addresses: ips}
				dials := 0
				d := Dialer{Policy: policy, Resolver: resolver, Connect: func(context.Context, string, string) (net.Conn, error) {
					dials++
					return nil, errors.New("must not dial")
				}}
				if _, err := d.DialContext(context.Background(), "tcp", "fixture.invalid:443"); !errors.Is(err, ErrDenied) || dials != 0 || resolver.calls != 1 {
					t.Fatalf("mixed DNS escaped: dials=%d lookups=%d err=%v", dials, resolver.calls, err)
				}
			})
		}
	}
	resolver := &fixedResolver{addresses: []netip.Addr{netip.MustParseAddr("203.0.113.1"), netip.MustParseAddr("2606:4700:4700::1111")}}
	var dials []string
	d := Dialer{Policy: policy, Resolver: resolver, Connect: func(_ context.Context, _, addr string) (net.Conn, error) {
		dials = append(dials, addr)
		return nil, errors.New("fixture unreachable")
	}}
	_, _ = d.DialContext(context.Background(), "tcp", "fixture.invalid:443")
	if strings.Join(dials, ",") != "203.0.113.1:443,[2606:4700:4700::1111]:443" || resolver.calls != 1 {
		t.Fatalf("unverified fallback or re-resolution: dials=%v lookups=%d", dials, resolver.calls)
	}
	// A later connection must revalidate the new DNS answer, not reuse a prior grant.
	resolver.addresses = []netip.Addr{netip.MustParseAddr("169.254.169.254")}
	if _, err := d.DialContext(context.Background(), "tcp", "fixture.invalid:443"); !errors.Is(err, ErrDenied) || len(dials) != 2 || resolver.calls != 2 {
		t.Fatalf("rebinding escaped revalidation: dials=%v lookups=%d err=%v", dials, resolver.calls, err)
	}
	resolver.addresses = nil
	if _, err := d.DialContext(context.Background(), "tcp", "fixture.invalid:443"); err == nil || len(dials) != 2 {
		t.Fatal("empty DNS answer allowed a connection")
	}
}
