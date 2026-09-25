package ruleset

import (
	"fmt"
	"strings"

	"github.com/sagernet/sing-box/option"
	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
)

const (
	domainRule = iota
	domainSuffixRule
	domainKeywordRule
	domainRegexRule
)

type domainItem struct {
	kind  int
	value string
}

// WriteGeosite writes one sing-box rule-set per geosite category.
// Domain types follow SagerNet/sing-geosite: root domains become an exact
// domain when they contain a dot, plus a leading-dot suffix.
func WriteGeosite(datPath string, list *routercommon.GeoSiteList) error {
	if list == nil {
		return fmt.Errorf("geosite list is nil")
	}

	for _, site := range list.GetEntry() {
		if site == nil {
			continue
		}

		rule, err := compileGeosite(site)
		if err != nil {
			return fmt.Errorf("category %q: %w", site.GetCountryCode(), err)
		}

		path, err := writeRuleSet(datPath, "geosite", site.GetCountryCode(), rule)
		if err != nil {
			return err
		}

		fmt.Printf("Written %s\n", path)
	}

	return nil
}

func compileGeosite(site *routercommon.GeoSite) (option.DefaultHeadlessRule, error) {
	items := make([]domainItem, 0, len(site.GetDomain())*2)
	seen := make(map[domainItem]struct{}, len(site.GetDomain())*2)

	for _, domain := range site.GetDomain() {
		if domain == nil {
			continue
		}

		value := strings.TrimSpace(domain.GetValue())
		if value == "" {
			return option.DefaultHeadlessRule{}, fmt.Errorf("empty domain value")
		}

		converted := domainItems(domain.GetType(), value)
		if converted == nil {
			return option.DefaultHeadlessRule{}, fmt.Errorf("unsupported domain type %v", domain.GetType())
		}

		for _, item := range converted {
			if _, exists := seen[item]; exists {
				continue
			}
			seen[item] = struct{}{}
			items = append(items, item)
		}
	}

	if len(items) == 0 {
		return option.DefaultHeadlessRule{}, fmt.Errorf("category contains no rules")
	}

	var rule option.DefaultHeadlessRule
	for _, item := range items {
		switch item.kind {
		case domainRule:
			rule.Domain = append(rule.Domain, item.value)
		case domainSuffixRule:
			rule.DomainSuffix = append(rule.DomainSuffix, item.value)
		case domainKeywordRule:
			rule.DomainKeyword = append(rule.DomainKeyword, item.value)
		case domainRegexRule:
			rule.DomainRegex = append(rule.DomainRegex, item.value)
		}
	}

	return rule, nil
}

func domainItems(domainType routercommon.Domain_Type, value string) []domainItem {
	switch domainType {
	case routercommon.Domain_Plain:
		return []domainItem{{kind: domainKeywordRule, value: value}}
	case routercommon.Domain_Regex:
		return []domainItem{{kind: domainRegexRule, value: value}}
	case routercommon.Domain_RootDomain:
		items := make([]domainItem, 0, 2)
		if strings.Contains(value, ".") {
			items = append(items, domainItem{kind: domainRule, value: value})
		}
		items = append(items, domainItem{kind: domainSuffixRule, value: "." + value})
		return items
	case routercommon.Domain_Full:
		return []domainItem{{kind: domainRule, value: value}}
	default:
		return nil
	}
}
