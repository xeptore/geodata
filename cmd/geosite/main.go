package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
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
	File       string   `json:"file"`
	Categories []string `json:"categories"`
}

type CachedGeoSiteFile struct {
	Path       string
	Categories map[string]*routercommon.GeoSite
}

type CategoryStats struct {
	Collected        int
	ExactDuplicates  int
	CoveredFullRules int
	CoveredRootRules int
	Retained         int
	SourceCategories int
}

func main() {
	configPath := flag.String("config", "geosite-config.json", "path to the JSON configuration file")

	flag.Parse()

	if err := run(*configPath); nil != err {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	config, err := loadConfig(configPath)
	if nil != err {
		return err
	}

	cache := make(map[string]*CachedGeoSiteFile)

	outputList := &routercommon.GeoSiteList{
		Entry: make([]*routercommon.GeoSite, 0, len(config.Categories)),
	}

	for _, outputConfig := range config.Categories {
		site, stats, err := buildOutputCategory(outputConfig, cache)
		if nil != err {
			return fmt.Errorf("build output category %q: %w", outputConfig.Name, err)
		}

		outputList.Entry = append(outputList.Entry, site)

		printCategoryStats(site.GetCountryCode(), stats)
	}

	sort.Slice(outputList.Entry, func(i, j int) bool {
		return outputList.Entry[i].GetCountryCode() < outputList.Entry[j].GetCountryCode()
	})

	if err := writeGeoSiteDAT(config.Output, outputList); nil != err {
		return err
	}

	if err := verifyGeoSiteDAT(config.Output); nil != err {
		return fmt.Errorf("verify generated file: %w", err)
	}

	fmt.Printf("\nWritten %d categories to %s\n", len(outputList.Entry), config.Output)

	return nil
}

func loadConfig(path string) (Config, error) {
	var config Config

	data, err := os.ReadFile(path)
	if nil != err {
		return config, fmt.Errorf("read config %q: %w", path, err)
	}

	if err := json.Unmarshal(data, &config); nil != err {
		return config, fmt.Errorf("decode config %q: %w", path, err)
	}

	config.Output = strings.TrimSpace(config.Output)
	if config.Output == "" {
		return config, errors.New("output cannot be empty")
	}

	if len(config.Categories) == 0 {
		return config, errors.New("at least one output category must be configured")
	}

	categoryNames := make(map[string]struct{})

	for categoryIndex, category := range config.Categories {
		name := normalizeCategoryCode(category.Name)
		if name == "" {
			return config, fmt.Errorf("categories[%d].name cannot be empty", categoryIndex)
		}

		if _, exists := categoryNames[name]; exists {
			return config, fmt.Errorf("duplicate output category %q", name)
		}

		categoryNames[name] = struct{}{}

		if len(category.Sources) == 0 {
			return config, fmt.Errorf("output category %q has no sources", name)
		}

		for sourceIndex, source := range category.Sources {
			if strings.TrimSpace(source.File) == "" {
				return config, fmt.Errorf("category %q source %d has an empty file path", name, sourceIndex)
			}

			if len(source.Categories) == 0 {
				return config, fmt.Errorf("category %q source %q has no categories", name, source.File)
			}

			for inputCategoryIndex, inputCategory := range source.Categories {
				if normalizeCategoryCode(inputCategory) == "" {
					return config, fmt.Errorf("category %q source %q category %d is empty", name, source.File, inputCategoryIndex)
				}
			}
		}
	}

	return config, nil
}

func buildOutputCategory(config OutputCategoryConfig, cache map[string]*CachedGeoSiteFile) (*routercommon.GeoSite, CategoryStats, error) {
	var stats CategoryStats
	var collected []*routercommon.Domain

	outputName := normalizeCategoryCode(config.Name)

	for _, sourceConfig := range config.Sources {
		sourceFile, err := getCachedGeoSiteFile(sourceConfig.File, cache)
		if nil != err {
			return nil, stats, err
		}

		for _, requestedCategory := range sourceConfig.Categories {
			categoryName := normalizeCategoryCode(requestedCategory)

			sourceSite := sourceFile.Categories[categoryName]
			if sourceSite == nil {
				return nil, stats, fmt.Errorf("category %q was not found in %q", categoryName, sourceConfig.File)
			}

			stats.SourceCategories++

			for _, domain := range sourceSite.GetDomain() {
				if domain == nil {
					continue
				}

				cloned := cloneDomainWithoutAttributes(domain)
				if cloned == nil {
					continue
				}

				collected = append(collected, cloned)
			}
		}
	}

	stats.Collected = len(collected)

	domains,
		exactDuplicates,
		coveredFullRules,
		coveredRootRules := normalizeDomains(collected)

	stats.ExactDuplicates = exactDuplicates
	stats.CoveredFullRules = coveredFullRules
	stats.CoveredRootRules = coveredRootRules
	stats.Retained = len(domains)

	return &routercommon.GeoSite{
		CountryCode: outputName,
		Domain:      domains,
	}, stats, nil
}

func getCachedGeoSiteFile(path string, cache map[string]*CachedGeoSiteFile) (*CachedGeoSiteFile, error) {
	cleanPath := filepath.Clean(path)

	if cached, exists := cache[cleanPath]; exists {
		return cached, nil
	}

	list, err := readGeoSiteDAT(cleanPath)
	if nil != err {
		return nil, err
	}

	categories := make(map[string]*routercommon.GeoSite)

	for _, site := range list.GetEntry() {
		if site == nil {
			continue
		}

		code := normalizeCategoryCode(site.GetCountryCode())
		if code == "" {
			continue
		}

		if _, exists := categories[code]; exists {
			return nil, fmt.Errorf("duplicate category %q in %q", code, cleanPath)
		}

		categories[code] = site
	}

	cached := &CachedGeoSiteFile{
		Path:       cleanPath,
		Categories: categories,
	}

	cache[cleanPath] = cached

	return cached, nil
}

func readGeoSiteDAT(path string) (*routercommon.GeoSiteList, error) {
	data, err := os.ReadFile(path)
	if nil != err {
		return nil, fmt.Errorf("read geosite file %q: %w", path, err)
	}

	var list routercommon.GeoSiteList
	if err := proto.Unmarshal(data, &list); nil != err {
		return nil, fmt.Errorf("unmarshal geosite file %q: %w", path, err)
	}

	return &list, nil
}

func writeGeoSiteDAT(path string, list *routercommon.GeoSiteList) error {
	if list == nil {
		return errors.New("output geosite list is nil")
	}

	outputDirectory := filepath.Dir(path)

	if outputDirectory != "." {
		if err := os.MkdirAll(outputDirectory, 0o755); nil != err {
			return fmt.Errorf("create output directory %q: %w", outputDirectory, err)
		}
	}

	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(list)
	if nil != err {
		return fmt.Errorf("marshal output geosite file: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); nil != err {
		return fmt.Errorf("write output geosite file %q: %w", path, err)
	}

	return nil
}

func verifyGeoSiteDAT(path string) error {
	list, err := readGeoSiteDAT(path)
	if nil != err {
		return err
	}

	if len(list.GetEntry()) == 0 {
		return errors.New("generated geosite file contains no categories")
	}

	for _, site := range list.GetEntry() {
		if site == nil {
			return errors.New("generated geosite file contains a nil category")
		}

		if normalizeCategoryCode(site.GetCountryCode()) == "" {
			return errors.New("generated geosite file contains an unnamed category")
		}

		if len(site.GetDomain()) == 0 {
			return fmt.Errorf("generated category %q contains no rules", site.GetCountryCode())
		}
	}

	return nil
}

func cloneDomainWithoutAttributes(domain *routercommon.Domain) *routercommon.Domain {
	if domain == nil {
		return nil
	}

	value := normalizeDomainValue(domain.GetType(), domain.GetValue())

	if value == "" {
		return nil
	}

	return &routercommon.Domain{
		Type:  domain.GetType(),
		Value: value,
	}
}

func normalizeDomainValue(
	domainType routercommon.Domain_Type,
	value string,
) string {
	value = strings.TrimSpace(value)

	switch domainType {
	case routercommon.Domain_RootDomain,
		routercommon.Domain_Full:
		value = strings.ToLower(value)
		value = strings.TrimRight(value, ".")
	}

	return value
}

func removeExactDuplicates(domains []*routercommon.Domain) ([]*routercommon.Domain, int) {
	unique := make(map[string]*routercommon.Domain, len(domains))

	duplicates := 0

	for _, domain := range domains {
		if domain == nil {
			continue
		}

		value := normalizeDomainValue(domain.GetType(), domain.GetValue())

		if value == "" {
			continue
		}

		normalized := &routercommon.Domain{
			Type:  domain.GetType(),
			Value: value,
		}

		key := domainKey(normalized)

		if _, exists := unique[key]; exists {
			duplicates++
			continue
		}

		unique[key] = normalized
	}

	result := make([]*routercommon.Domain, 0, len(unique))

	for _, domain := range unique {
		result = append(result, domain)
	}

	sort.Slice(result, func(i, j int) bool {
		left := result[i]
		right := result[j]

		if left.GetType() != right.GetType() {
			return left.GetType() < right.GetType()
		}

		return left.GetValue() < right.GetValue()
	})

	return result, duplicates
}

func normalizeDomains(
	domains []*routercommon.Domain,
) (
	result []*routercommon.Domain,
	exactDuplicates int,
	coveredFullRules int,
	coveredRootRules int,
) {
	unique := make(
		map[string]*routercommon.Domain,
		len(domains),
	)

	// First pass: normalize and remove exact duplicates.
	for _, domain := range domains {
		if domain == nil {
			continue
		}

		value := normalizeDomainValue(
			domain.GetType(),
			domain.GetValue(),
		)

		if value == "" {
			continue
		}

		normalized := &routercommon.Domain{
			Type:  domain.GetType(),
			Value: value,
		}

		key := domainKey(normalized)

		if _, exists := unique[key]; exists {
			exactDuplicates++
			continue
		}

		unique[key] = normalized
	}

	// Collect all RootDomain rules.
	rootDomains := make(
		map[string]struct{},
		len(unique),
	)

	for _, domain := range unique {
		if domain.GetType() != routercommon.Domain_RootDomain {
			continue
		}

		rootDomains[domain.GetValue()] = struct{}{}
	}

	result = make(
		[]*routercommon.Domain,
		0,
		len(unique),
	)

	// Second pass: remove semantically covered rules.
	for _, domain := range unique {
		switch domain.GetType() {
		case routercommon.Domain_Full:
			// full:api.example.com is covered by:
			//
			// domain:api.example.com
			// domain:example.com
			if coveredByRootDomain(
				domain.GetValue(),
				rootDomains,
			) {
				coveredFullRules++
				continue
			}

		case routercommon.Domain_RootDomain:
			// domain:api.example.com is covered by:
			//
			// domain:example.com
			if coveredByParentRootDomain(
				domain.GetValue(),
				rootDomains,
			) {
				coveredRootRules++
				continue
			}
		}

		result = append(result, domain)
	}

	sort.Slice(result, func(i, j int) bool {
		left := result[i]
		right := result[j]

		if left.GetType() != right.GetType() {
			return left.GetType() < right.GetType()
		}

		return left.GetValue() < right.GetValue()
	})

	return result,
		exactDuplicates,
		coveredFullRules,
		coveredRootRules
}

func coveredByRootDomain(
	hostname string,
	rootDomains map[string]struct{},
) bool {
	current := hostname

	for {
		if _, exists := rootDomains[current]; exists {
			return true
		}

		dot := strings.IndexByte(current, '.')
		if dot == -1 {
			return false
		}

		current = current[dot+1:]
	}
}

func coveredByParentRootDomain(
	domain string,
	rootDomains map[string]struct{},
) bool {
	current := domain

	for {
		dot := strings.IndexByte(current, '.')
		if dot == -1 {
			return false
		}

		current = current[dot+1:]

		if _, exists := rootDomains[current]; exists {
			return true
		}
	}
}

func normalizeCategoryCode(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func domainKey(domain *routercommon.Domain) string {
	return fmt.Sprintf("%d\x00%s", domain.GetType(), domain.GetValue())
}
func printCategoryStats(
	name string,
	stats CategoryStats,
) {
	fmt.Printf("%s:\n", name)

	fmt.Printf(
		"  source categories:        %d\n",
		stats.SourceCategories,
	)

	fmt.Printf(
		"  collected rules:          %d\n",
		stats.Collected,
	)

	fmt.Printf(
		"  exact duplicates removed: %d\n",
		stats.ExactDuplicates,
	)

	fmt.Printf(
		"  covered full removed:      %d\n",
		stats.CoveredFullRules,
	)

	fmt.Printf(
		"  covered root removed:      %d\n",
		stats.CoveredRootRules,
	)

	fmt.Printf(
		"  retained rules:           %d\n",
		stats.Retained,
	)
}
