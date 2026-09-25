package ruleset

import (
	"bytes"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagernet/sing-box/common/srs"
	"github.com/sagernet/sing/common/domain"
	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
)

func TestWriteGeositeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	datPath := filepath.Join(dir, "geosite.dat")

	list := &routercommon.GeoSiteList{
		Entry: []*routercommon.GeoSite{{
			CountryCode: "BLOCK",
			Domain: []*routercommon.Domain{
				{Type: routercommon.Domain_RootDomain, Value: "example.com"},
				{Type: routercommon.Domain_Full, Value: "exact.test"},
				{Type: routercommon.Domain_Plain, Value: "ads"},
				{Type: routercommon.Domain_Regex, Value: `^track\.`},
				{Type: routercommon.Domain_RootDomain, Value: "cn"},
			},
		}},
	}

	if err := WriteGeosite(datPath, list); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "geosite-block.srs"))
	if err != nil {
		t.Fatal(err)
	}

	if len(data) < 4 || string(data[:3]) != "SRS" || data[3] != RuleSetVersion {
		t.Fatalf("unexpected srs header %x", data[:4])
	}

	recovered, err := srs.Read(bytes.NewReader(data), true)
	if err != nil {
		t.Fatal(err)
	}

	if len(recovered.Options.Rules) != 1 {
		t.Fatalf("rules = %d", len(recovered.Options.Rules))
	}

	rule := recovered.Options.Rules[0].DefaultOptions
	matcher := domain.NewMatcher(rule.Domain, rule.DomainSuffix, false)

	for _, host := range []string{"example.com", "www.example.com", "exact.test", "a.cn"} {
		if !matcher.Match(host) {
			t.Errorf("expected match %s", host)
		}
	}

	if matcher.Match("cn") {
		t.Error("root domain without a dot must not match the bare label")
	}

	if !contains(rule.DomainKeyword, "ads") {
		t.Errorf("keywords = %v", rule.DomainKeyword)
	}
	if !contains(rule.DomainRegex, `^track\.`) {
		t.Errorf("regex = %v", rule.DomainRegex)
	}
}

func TestWriteGeoIPRoundTrip(t *testing.T) {
	dir := t.TempDir()
	datPath := filepath.Join(dir, "geoip.dat")

	list := &routercommon.GeoIPList{
		Entry: []*routercommon.GeoIP{{
			CountryCode: "DIRECT",
			Cidr: []*routercommon.CIDR{
				{Ip: []byte{10, 1, 2, 3}, Prefix: 8},
				{Ip: netip.MustParseAddr("2001:db8::1").AsSlice(), Prefix: 32},
			},
		}},
	}

	if err := WriteGeoIP(datPath, list); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "geoip-direct.srs"))
	if err != nil {
		t.Fatal(err)
	}

	if len(data) < 4 || string(data[:3]) != "SRS" || data[3] != RuleSetVersion {
		t.Fatalf("unexpected srs header %x", data[:4])
	}

	recovered, err := srs.Read(bytes.NewReader(data), true)
	if err != nil {
		t.Fatal(err)
	}

	rule := recovered.Options.Rules[0].DefaultOptions
	set := rule.IPSet
	if set == nil || !set.Contains(netip.MustParseAddr("10.9.9.9")) {
		t.Fatalf("missing ipv4 prefix in %v", rule.IPCIDR)
	}
	if !set.Contains(netip.MustParseAddr("2001:db8::1")) {
		t.Fatalf("missing ipv6 prefix in %v", rule.IPCIDR)
	}
	if set.Contains(netip.MustParseAddr("11.0.0.1")) {
		t.Fatal("unexpected ipv4 match")
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
