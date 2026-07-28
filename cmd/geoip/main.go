package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/v2fly/v2ray-core/v5/app/router/routercommon"
	"google.golang.org/protobuf/proto"
)

type Config struct {
	Output     string                 `json:"output"`
	Categories []OutputCategoryConfig `json:"categories"`
}

type OutputCategoryConfig struct {
	Name    string         `json:"name"`
	Sources []SourceConfig `json:"sources"`
}

type SourceConfig struct {
	File          string   `json:"file"`
	Categories    []string `json:"categories"`
	IgnoreMissing bool     `json:"ignore_missing"`
}

type CachedGeoIPFile struct {
	Categories map[string]*routercommon.GeoIP
}

type CategoryStats struct {
	SourceCategories int
	Collected        int
	Invalid          int
	ExactDuplicates  int
	CoveredNetworks  int
	Retained         int
}

func main() {
	configPath := flag.String(
		"config",
		"geoip-config.json",
		"path to the JSON configuration",
	)

	flag.Parse()

	if err := run(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	config, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	cache := make(map[string]*CachedGeoIPFile)

	output := &routercommon.GeoIPList{
		Entry: make([]*routercommon.GeoIP, 0, len(config.Categories)),
	}

	for _, categoryConfig := range config.Categories {
		category, stats, err := buildOutputCategory(
			categoryConfig,
			cache,
		)
		if err != nil {
			return fmt.Errorf(
				"build category %q: %w",
				categoryConfig.Name,
				err,
			)
		}

		output.Entry = append(output.Entry, category)
		printStats(category.GetCountryCode(), stats)
	}

	sort.Slice(output.Entry, func(i, j int) bool {
		return output.Entry[i].GetCountryCode() <
			output.Entry[j].GetCountryCode()
	})

	if err := writeGeoIPDAT(config.Output, output); err != nil {
		return err
	}

	if err := verifyGeoIPDAT(config.Output); err != nil {
		return fmt.Errorf("verify output: %w", err)
	}

	fmt.Printf(
		"\nWritten %d categories to %s\n",
		len(output.Entry),
		config.Output,
	)

	return nil
}

func loadConfig(path string) (Config, error) {
	var config Config

	data, err := os.ReadFile(path)
	if err != nil {
		return config, fmt.Errorf("read config %q: %w", path, err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("decode config %q: %w", path, err)
	}

	config.Output = strings.TrimSpace(config.Output)
	if config.Output == "" {
		return config, errors.New("output cannot be empty")
	}

	if len(config.Categories) == 0 {
		return config, errors.New("no output categories configured")
	}

	names := make(map[string]struct{})

	for index, category := range config.Categories {
		name := normalizeCategoryCode(category.Name)

		if name == "" {
			return config, fmt.Errorf(
				"categories[%d].name cannot be empty",
				index,
			)
		}

		if _, exists := names[name]; exists {
			return config, fmt.Errorf(
				"duplicate output category %q",
				name,
			)
		}

		names[name] = struct{}{}

		if len(category.Sources) == 0 {
			return config, fmt.Errorf(
				"category %q has no sources",
				name,
			)
		}

		for sourceIndex, source := range category.Sources {
			if strings.TrimSpace(source.File) == "" {
				return config, fmt.Errorf(
					"category %q source %d has an empty file",
					name,
					sourceIndex,
				)
			}

			if len(source.Categories) == 0 {
				return config, fmt.Errorf(
					"category %q source %q has no categories",
					name,
					source.File,
				)
			}
		}
	}

	return config, nil
}

func buildOutputCategory(
	config OutputCategoryConfig,
	cache map[string]*CachedGeoIPFile,
) (*routercommon.GeoIP, CategoryStats, error) {
	var stats CategoryStats
	var collected []*routercommon.CIDR

	for _, sourceConfig := range config.Sources {
		source, err := getCachedGeoIPFile(sourceConfig.File, cache)
		if err != nil {
			return nil, stats, err
		}

		for _, requestedName := range sourceConfig.Categories {
			categoryName := normalizeCategoryCode(requestedName)
			sourceCategory := source.Categories[categoryName]

			if sourceCategory == nil {
				if sourceConfig.IgnoreMissing {
					fmt.Fprintf(
						os.Stderr,
						"warning: category %q not found in %q\n",
						categoryName,
						sourceConfig.File,
					)
					continue
				}

				return nil, stats, fmt.Errorf(
					"category %q was not found in %q",
					categoryName,
					sourceConfig.File,
				)
			}

			if sourceCategory.GetInverseMatch() {
				return nil, stats, fmt.Errorf(
					"category %q in %q uses inverse_match",
					categoryName,
					sourceConfig.File,
				)
			}

			stats.SourceCategories++

			for _, cidr := range sourceCategory.GetCidr() {
				if cidr != nil {
					collected = append(collected, cidr)
				}
			}
		}
	}

	stats.Collected = len(collected)

	normalized, normalizationStats := normalizeCIDRs(collected)

	stats.Invalid = normalizationStats.Invalid
	stats.ExactDuplicates = normalizationStats.ExactDuplicates
	stats.CoveredNetworks = normalizationStats.CoveredNetworks
	stats.Retained = len(normalized)

	return &routercommon.GeoIP{
		CountryCode: normalizeCategoryCode(config.Name),
		Cidr:        normalized,
	}, stats, nil
}

func getCachedGeoIPFile(
	path string,
	cache map[string]*CachedGeoIPFile,
) (*CachedGeoIPFile, error) {
	cleanPath := filepath.Clean(path)

	if cached, exists := cache[cleanPath]; exists {
		return cached, nil
	}

	list, err := readGeoIPDAT(cleanPath)
	if err != nil {
		return nil, err
	}

	categories := make(map[string]*routercommon.GeoIP)

	for _, entry := range list.GetEntry() {
		if entry == nil {
			continue
		}

		code := normalizeCategoryCode(entry.GetCountryCode())
		if code == "" {
			continue
		}

		if _, exists := categories[code]; exists {
			return nil, fmt.Errorf(
				"duplicate category %q in %q",
				code,
				cleanPath,
			)
		}

		categories[code] = entry
	}

	cached := &CachedGeoIPFile{
		Categories: categories,
	}

	cache[cleanPath] = cached

	return cached, nil
}

func readGeoIPDAT(path string) (*routercommon.GeoIPList, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf(
			"read geoip file %q: %w",
			path,
			err,
		)
	}

	var list routercommon.GeoIPList

	if err := proto.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf(
			"unmarshal geoip file %q: %w",
			path,
			err,
		)
	}

	return &list, nil
}

func writeGeoIPDAT(
	path string,
	list *routercommon.GeoIPList,
) error {
	if list == nil {
		return errors.New("output geoip list is nil")
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf(
				"create output directory %q: %w",
				dir,
				err,
			)
		}
	}

	data, err := proto.MarshalOptions{
		Deterministic: true,
	}.Marshal(list)
	if err != nil {
		return fmt.Errorf("marshal geoip file: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write geoip file %q: %w", path, err)
	}

	return nil
}

func verifyGeoIPDAT(path string) error {
	list, err := readGeoIPDAT(path)
	if err != nil {
		return err
	}

	if len(list.GetEntry()) == 0 {
		return errors.New("generated file has no categories")
	}

	for _, category := range list.GetEntry() {
		if category == nil {
			return errors.New("generated file contains a nil category")
		}

		if normalizeCategoryCode(category.GetCountryCode()) == "" {
			return errors.New("generated file has an unnamed category")
		}

		if len(category.GetCidr()) == 0 {
			return fmt.Errorf(
				"generated category %q contains no CIDRs",
				category.GetCountryCode(),
			)
		}
	}

	return nil
}

type NormalizationStats struct {
	Invalid         int
	ExactDuplicates int
	CoveredNetworks int
}

func normalizeCIDRs(
	cidrs []*routercommon.CIDR,
) ([]*routercommon.CIDR, NormalizationStats) {
	var stats NormalizationStats

	unique := make(map[netip.Prefix]struct{}, len(cidrs))

	for _, cidr := range cidrs {
		prefix, err := protoCIDRToPrefix(cidr)
		if err != nil {
			stats.Invalid++
			continue
		}

		// Mask removes host bits, producing the canonical network address.
		prefix = prefix.Masked()

		if _, exists := unique[prefix]; exists {
			stats.ExactDuplicates++
			continue
		}

		unique[prefix] = struct{}{}
	}

	prefixes := make([]netip.Prefix, 0, len(unique))

	for prefix := range unique {
		prefixes = append(prefixes, prefix)
	}

	/*
		Sort broadest networks first.

		IPv4 and IPv6 are separated by address bit length. Within each
		address family, shorter prefixes come first.
	*/
	sort.Slice(prefixes, func(i, j int) bool {
		left := prefixes[i]
		right := prefixes[j]

		leftBits := left.Addr().BitLen()
		rightBits := right.Addr().BitLen()

		if leftBits != rightBits {
			return leftBits < rightBits
		}

		if left.Bits() != right.Bits() {
			return left.Bits() < right.Bits()
		}

		return left.Addr().Compare(right.Addr()) < 0
	})

	retained := make([]netip.Prefix, 0, len(prefixes))

	for _, candidate := range prefixes {
		if coveredByExistingPrefix(candidate, retained) {
			stats.CoveredNetworks++
			continue
		}

		retained = append(retained, candidate)
	}

	// Sort final output by address, then prefix.
	sort.Slice(retained, func(i, j int) bool {
		left := retained[i]
		right := retained[j]

		if left.Addr().BitLen() != right.Addr().BitLen() {
			return left.Addr().BitLen() <
				right.Addr().BitLen()
		}

		if comparison := left.Addr().Compare(right.Addr()); comparison != 0 {
			return comparison < 0
		}

		return left.Bits() < right.Bits()
	})

	result := make([]*routercommon.CIDR, 0, len(retained))

	for _, prefix := range retained {
		result = append(result, prefixToProtoCIDR(prefix))
	}

	return result, stats
}

func coveredByExistingPrefix(
	candidate netip.Prefix,
	retained []netip.Prefix,
) bool {
	for _, parent := range retained {
		if parent.Addr().BitLen() != candidate.Addr().BitLen() {
			continue
		}

		if parent.Bits() > candidate.Bits() {
			continue
		}

		if parent.Contains(candidate.Addr()) {
			return true
		}
	}

	return false
}

func protoCIDRToPrefix(
	cidr *routercommon.CIDR,
) (netip.Prefix, error) {
	if cidr == nil {
		return netip.Prefix{}, errors.New("nil CIDR")
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
		return netip.Prefix{}, fmt.Errorf(
			"invalid IP byte length %d",
			len(cidr.GetIp()),
		)
	}

	prefixBits := int(cidr.GetPrefix())

	if prefixBits < 0 || prefixBits > address.BitLen() {
		return netip.Prefix{}, fmt.Errorf(
			"invalid prefix %d for %s",
			prefixBits,
			address,
		)
	}

	return netip.PrefixFrom(address, prefixBits).Masked(), nil
}

func prefixToProtoCIDR(prefix netip.Prefix) *routercommon.CIDR {
	address := prefix.Masked().Addr()

	var ip []byte

	if address.Is4() {
		bytes := address.As4()
		ip = append([]byte(nil), bytes[:]...)
	} else {
		bytes := address.As16()
		ip = append([]byte(nil), bytes[:]...)
	}

	return &routercommon.CIDR{
		Ip:     ip,
		Prefix: uint32(prefix.Bits()),
	}
}

func normalizeCategoryCode(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func printStats(name string, stats CategoryStats) {
	fmt.Printf("%s:\n", name)
	fmt.Printf("  source categories:        %d\n", stats.SourceCategories)
	fmt.Printf("  collected CIDRs:          %d\n", stats.Collected)
	fmt.Printf("  invalid CIDRs skipped:    %d\n", stats.Invalid)
	fmt.Printf("  exact duplicates removed: %d\n", stats.ExactDuplicates)
	fmt.Printf("  covered networks removed: %d\n", stats.CoveredNetworks)
	fmt.Printf("  retained CIDRs:           %d\n", stats.Retained)
}
