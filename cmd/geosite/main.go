package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/xeptore/geodata/internal/dat"
	"github.com/xeptore/geodata/internal/ruleset"
)

func main() {
	configPath := flag.String("config", "geosite-config.json", "path to the JSON configuration file")

	flag.Parse()

	if err := run(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	output, list, err := dat.WriteGeosite(configPath)
	if err != nil {
		return err
	}

	if err := ruleset.WriteGeosite(output, list); err != nil {
		return err
	}

	fmt.Printf("\nWritten %d categories to %s\n", len(list.GetEntry()), output)

	return nil
}
