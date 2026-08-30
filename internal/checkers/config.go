package checkers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bcmk/siren/v5/lib/cmdlib"
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

// readCheckerConfig loads <website>-checker.json.
// When checkerCfgPath is empty, searches the user config directories.
// XRN_-prefixed env vars override file values (e.g. XRN_CLIENT_SECRET).
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
	cmdlib.Linf("successfully read checker config %q", resolvedPath)

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
	return cmdlib.FindConfig(website + "-checker.json")
}
