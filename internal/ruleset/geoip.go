package ruleset

import (
	"fmt"
	"net/netip"
	"sort"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
)

// WriteGeoIP writes one sing-box rule-set per GeoIP category.
func WriteGeoIP(datPath string, list *routercommon.GeoIPList) error {
	if list == nil {
		return fmt.Errorf("geoip list is nil")
	}

	for _, entry := range list.GetEntry() {
		if entry == nil {
			continue
		}

		rule, err := compileGeoIP(entry)
		if err != nil {
			return fmt.Errorf("category %q: %w", entry.GetCountryCode(), err)
		}

		path, err := writeRuleSet(datPath, "geoip", entry.GetCountryCode(), rule)
		if err != nil {
			return err
		}

		fmt.Printf("Written %s\n", path)
	}

	return nil
}

func compileGeoIP(entry *routercommon.GeoIP) (option.DefaultHeadlessRule, error) {
	cidrs := make([]string, 0, len(entry.GetCidr()))
	seen := make(map[string]struct{}, len(entry.GetCidr()))

	for _, cidr := range entry.GetCidr() {
		prefix, err := cidrPrefix(cidr)
		if err != nil {
			return option.DefaultHeadlessRule{}, err
		}

		value := prefix.String()
		if _, exists := seen[value]; exists {
			continue
		}

		seen[value] = struct{}{}
		cidrs = append(cidrs, value)
	}

	if len(cidrs) == 0 {
		return option.DefaultHeadlessRule{}, fmt.Errorf("category contains no CIDRs")
	}

	sort.Strings(cidrs)

	return option.DefaultHeadlessRule{
		IPCIDR: badoption.Listable[string](cidrs),
	}, nil
}

func cidrPrefix(cidr *routercommon.CIDR) (netip.Prefix, error) {
	if cidr == nil {
		return netip.Prefix{}, fmt.Errorf("nil CIDR")
	}

	var address netip.Addr

	switch len(cidr.GetIp()) {
	case 4:
		var bytes [4]byte
		copy(bytes[:], cidr.GetIp())
		address = netip.AddrFrom4(bytes)
	case 16:
		var bytes [16]byte
		copy(bytes[:], cidr.GetIp())
		address = netip.AddrFrom16(bytes)
	default:
		return netip.Prefix{}, fmt.Errorf("invalid IP byte length %d", len(cidr.GetIp()))
	}

	bits := int(cidr.GetPrefix())
	if bits < 0 || bits > address.BitLen() {
		return netip.Prefix{}, fmt.Errorf("invalid prefix %d for %s", bits, address)
	}

	return netip.PrefixFrom(address, bits).Masked(), nil
}
