package checkers

import (
	"fmt"
	"slices"
)

// constructors lists every site checker. Add a new site here.
var constructors = []func() Checker{
	func() Checker { return &RandomChecker{} },
	func() Checker { return &BongaCamsChecker{} },
	func() Checker { return &ChaturbateChecker{} },
	func() Checker { return &Cam4Checker{} },
	func() Checker { return &CamSodaChecker{} },
	func() Checker { return &Flirt4FreeChecker{} },
	func() Checker { return &StreamateChecker{} },
	func() Checker { return NewMyFreeCamsChecker() },
	func() Checker { return &LiveJasminChecker{} },
	func() Checker { return &StripchatChecker{} },
	func() Checker { return &TwitchChecker{} },
	func() Checker { return &KickChecker{} },
}

// New returns a fresh (pre-Init) checker for the given site.
func New(site string) (Checker, error) {
	for _, f := range constructors {
		if c := f(); c.Site() == site {
			return c, nil
		}
	}
	return nil, fmt.Errorf("unknown site %q", site)
}

// Build is New + Init.
func Build(site, checkerCfgPath string, dbg bool) (Checker, error) {
	checker, err := New(site)
	if err != nil {
		return nil, err
	}
	if err := checker.Init(checkerCfgPath, dbg); err != nil {
		return nil, err
	}
	return checker, nil
}

// CLISites returns CLI-capable site names, sorted.
func CLISites() []string {
	var out []string
	for _, f := range constructors {
		c := f()
		if c.Capabilities().SupportsCLI {
			out = append(out, c.Site())
		}
	}
	slices.Sort(out)
	return out
}
