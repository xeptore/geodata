package ruleset

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// RuleSetVersion is the binary version sing-box writes for domain and IP
// rule-sets. Newer item types are absent, so this stays compatible with
// sing-box 1.10 and later.
const RuleSetVersion = C.RuleSetVersion2

func writeRuleSet(datPath, kind, category string, rule option.DefaultHeadlessRule) (string, error) {
	name := strings.ToLower(strings.TrimSpace(category))
	if name == "" {
		return "", fmt.Errorf("empty %s category name", kind)
	}

	outputPath := filepath.Join(filepath.Dir(datPath), kind+"-"+name+".srs")

	file, err := os.Create(outputPath)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", outputPath, err)
	}

	ruleSet := option.PlainRuleSet{
		Rules: []option.HeadlessRule{{
			Type:           C.RuleTypeDefault,
			DefaultOptions: rule,
		}},
	}

	err = srs.Write(file, ruleSet, RuleSetVersion)
	closeErr := file.Close()
	if err != nil {
		os.Remove(outputPath)
		return "", fmt.Errorf("write %s: %w", outputPath, err)
	}
	if closeErr != nil {
		os.Remove(outputPath)
		return "", fmt.Errorf("close %s: %w", outputPath, closeErr)
	}

	return outputPath, nil
}
