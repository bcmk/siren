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
	InputIcons   map[string]Icon `json:"-"`
	FinalIcons   map[string]Icon `json:"-"`
	VisibleIcons map[string]bool `json:"-"`
}

// IconV2 represents icon from config v2
type IconV2 struct {
	Version int     `json:"version,omitempty"`
	Width   float64 `json:"width,omitempty"`
	Height  float64 `json:"height,omitempty"`
}

// PackV2 represents an icon pack from config v2
type PackV2 struct {
	HumanName            string            `json:"human_name"`
	Scale                int               `json:"scale"`
	ChaturbateIconsScale *int              `json:"chaturbate_icons_scale,omitempty"`
	VGap                 *int              `json:"vgap,omitempty"`
	HGap                 *int              `json:"hgap,omitempty"`
	Disable              bool              `json:"disable"`
	FinalType            string            `json:"final_type"`
	Timestamp            int64             `json:"timestamp"`
	InputType            string            `json:"input_type"`
	Icons                map[string]IconV2 `json:"icons"`

	Name string `json:"-"`
}

// Config represents site or converter config
type Config struct {
	ConnectionString string `yaml:"connection_string"`
	ListenAddress    string `yaml:"listen_address"`
	BaseURL          string `yaml:"base_url"`
	BaseDomain       string `yaml:"base_domain"`
	BucketName       string `yaml:"bucket_name"`
	BucketRegion     string `yaml:"bucket_region"`
	BucketEndpoint   string `yaml:"bucket_endpoint"`
	BucketAccessKey  string `yaml:"bucket_access_key"`
	BucketSecretKey  string `yaml:"bucket_secret_key"`
	BaseBucketURL    string `yaml:"base_bucket_url"`
	Debug            bool   `yaml:"debug"`
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
