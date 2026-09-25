package dat

import "strings"

// Config is the shared JSON shape for geosite and geoip generation.
type Config struct {
	Output     string     `json:"output"`
	Categories []Category `json:"categories"`
}

// Category is one output list built from one or more source files.
type Category struct {
	Name    string   `json:"name"`
	Sources []Source `json:"sources"`
}

// Source selects categories from a single input .dat file.
// IgnoreMissing is used by geoip generation.
type Source struct {
	File          string   `json:"file"`
	Categories    []string `json:"categories"`
	IgnoreMissing bool     `json:"ignore_missing"`
}

func normalizeCategoryCode(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}
