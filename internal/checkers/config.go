package checkers

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bcmk/siren/v2/lib/cmdlib"
	"github.com/spf13/viper"
)

type validatedCheckerConfig interface {
	validate() error
}

// SimpleCheckerConfig holds a single public online URL and no
// per-site secrets.
type SimpleCheckerConfig struct {
	BaseCheckerConfig   `mapstructure:",squash"`
	UsersOnlineEndpoint string      `mapstructure:"users_online_endpoint"`
	Headers             [][2]string `mapstructure:"headers"`
}

func (c *SimpleCheckerConfig) validate() error {
	if err := c.validateBase(); err != nil {
		return err
	}
	if c.UsersOnlineEndpoint == "" {
		return errors.New("configure users_online_endpoint")
	}
	return nil
}

// readCheckerConfig loads <website>-checker.json. When checkerCfgPath
// is empty, searches the CWD then ~/.config/siren/. XRN_-prefixed env
// vars override file values (e.g. XRN_CLIENT_SECRET).
func readCheckerConfig(
	cfg validatedCheckerConfig,
	website, checkerCfgPath string,
) error {
	resolvedPath := checkerCfgPath
	if resolvedPath == "" {
		var err error
		resolvedPath, err = findCheckerConfig(website)
		if err != nil {
			return err
		}
	}

	v := viper.New()
	v.SetConfigFile(resolvedPath)
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("reading %q: %w", resolvedPath, err)
	}
	log.Printf("successfully read checker config %q", resolvedPath)

	v.SetEnvPrefix("XRN")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	cmdlib.BindEnvForConfig(v, cfg)

	if err := v.Unmarshal(cfg, cmdlib.StrictConfigDecoder); err != nil {
		return fmt.Errorf("parsing %q: %w", resolvedPath, err)
	}
	if err := cfg.validate(); err != nil {
		return fmt.Errorf("validating %q: %w", resolvedPath, err)
	}
	return nil
}

func findCheckerConfig(website string) (string, error) {
	name := website + "-checker.json"
	var dirs []string
	addDistinctDir := func(d string) {
		if !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	addDistinctDir(".")
	if cfg, err := os.UserConfigDir(); err == nil {
		addDistinctDir(filepath.Join(cfg, "siren"))
	}
	// Also search ~/.config/siren on macOS, where os.UserConfigDir
	// points at ~/Library/Application Support; on Linux it's already
	// that path, so addDistinctDir drops the duplicate.
	if home, err := os.UserHomeDir(); err == nil {
		addDistinctDir(filepath.Join(home, ".config", "siren"))
	}
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("checker config %q not found in %v", name, dirs)
}
