package sitelib

import (
	"os"
	"path/filepath"

	"github.com/bcmk/siren/lib"
	"gopkg.in/yaml.v2"
)

// Pack represents an icon pack
type Pack struct {
	Name             string `yaml:"name"`
	Scale            int    `yaml:"scale"`
	VerticalPosition int    `yaml:"vertical_position"`
	Margin           *int   `yaml:"margin"`
	InputType        string `yaml:"input_type"`
	FinalType        string `yaml:"final_type"`
	FinalHeight      int    `yaml:"final_height"`
	Icons            []Icon `yaml:"icons"`
	Disable          bool   `yaml:"disable"`
	Banner           string `yaml:"banner"`
}

// Config represents site or converter config
type Config struct {
	DBPath        string `yaml:"db_path"`
	ListenAddress string `yaml:"listen_address"`
	BaseURL       string `yaml:"base_url"`
	Input         string `yaml:"input"`
	Files         string `yaml:"files"`
	Packs         []Pack `yaml:"packs"`
	BaseDomain    string `yaml:"base_domain"`
	Debug         bool   `yaml:"debug"`
}

// ReadConfig reads config file and parses it
func ReadConfig(path string) Config {
	file, err := os.Open(filepath.Clean(path))
	lib.CheckErr(err)
	defer func() { lib.CheckErr(file.Close()) }()
	decoder := yaml.NewDecoder(file)
	parsed := Config{}
	err = decoder.Decode(&parsed)
	lib.CheckErr(err)
	return parsed
}
