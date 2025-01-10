// Package sitelib provides a library for siren sites
package sitelib

import (
	"os"
	"path/filepath"

	"github.com/bcmk/siren/lib/cmdlib"
	"gopkg.in/yaml.v3"
)

// Icon represents all required icon's fields
type Icon struct {
	VersionedName    string
	NotVersionedName string
	Width            float64
	Height           float64
}

// Pack represents an icon pack
type Pack struct {
	HumanName            string   `json:"human_name"`
	Scale                int      `json:"scale"`
	ChaturbateIconsScale *int     `json:"chaturbate_icons_scale"`
	VGap                 *int     `json:"vgap"`
	HGap                 *int     `json:"hgap"`
	Disable              bool     `json:"disable"`
	Version              int      `json:"version"`
	FinalType            string   `json:"final_type"`
	HiddenIcons          []string `json:"hidden_icons"`
	Timestamp            int64    `json:"timestamp"`
	InputType            string   `json:"input_type"`

	Name         string          `json:"-"`
	SourceIcons  map[string]Icon `json:"-"`
	InputIcons   map[string]Icon `json:"-"`
	FinalIcons   map[string]Icon `json:"-"`
	VisibleIcons map[string]bool `json:"-"`
}

// Config represents site or converter config
type Config struct {
	DBPath        string `yaml:"db_path"`
	ListenAddress string `yaml:"listen_address"`
	BaseURL       string `yaml:"base_url"`
	Files         string `yaml:"files"`
	BaseDomain    string `yaml:"base_domain"`
	Debug         bool   `yaml:"debug"`
}

// ReadConfig reads config file and parses it
func ReadConfig(path string) Config {
	file, err := os.Open(filepath.Clean(path))
	cmdlib.CheckErr(err)
	defer func() { cmdlib.CheckErr(file.Close()) }()
	decoder := yaml.NewDecoder(file)
	parsed := Config{}
	err = decoder.Decode(&parsed)
	cmdlib.CheckErr(err)
	return parsed
}
