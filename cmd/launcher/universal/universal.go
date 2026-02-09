package universal

import (
	"context"
	"fmt"
	"strings"

	"github.com/OnslaughtSnail/caelis/cmd/launcher"
)

type uniLauncher struct {
	chosen       launcher.SubLauncher
	sublaunchers []launcher.SubLauncher
}

func NewLauncher(sublaunchers ...launcher.SubLauncher) launcher.Launcher {
	return &uniLauncher{sublaunchers: sublaunchers}
}

func (u *uniLauncher) Execute(ctx context.Context, args []string) error {
	rest, err := u.parse(args)
	if err != nil {
		return err
	}
	if err := ErrorOnUnparsedArgs(rest); err != nil {
		return err
	}
	if u.chosen == nil {
		return fmt.Errorf("launcher: no sublauncher selected")
	}
	return u.chosen.Run(ctx)
}

func (u *uniLauncher) parse(args []string) ([]string, error) {
	if len(u.sublaunchers) == 0 {
		return nil, fmt.Errorf("launcher: no sublaunchers configured")
	}
	keyToLauncher := map[string]launcher.SubLauncher{}
	for _, one := range u.sublaunchers {
		if one == nil {
			continue
		}
		key := one.Keyword()
		if key == "" {
			return nil, fmt.Errorf("launcher: empty keyword")
		}
		if _, exists := keyToLauncher[key]; exists {
			return nil, fmt.Errorf("launcher: duplicate keyword %q", key)
		}
		keyToLauncher[key] = one
	}
	if len(keyToLauncher) == 0 {
		return nil, fmt.Errorf("launcher: no valid sublaunchers")
	}

	u.chosen = u.sublaunchers[0]
	if len(args) == 0 {
		return u.chosen.Parse(args)
	}
	if byKey, ok := keyToLauncher[args[0]]; ok {
		u.chosen = byKey
		return u.chosen.Parse(args[1:])
	}
	return u.chosen.Parse(args)
}

func (u *uniLauncher) CommandLineSyntax() string {
	var b strings.Builder
	b.WriteString("Usage:\n")
	b.WriteString("  <program> [mode] [flags]\n\n")
	b.WriteString("Modes:\n")
	for _, one := range u.sublaunchers {
		if one == nil {
			continue
		}
		fmt.Fprintf(&b, "  %s\t%s\n", one.Keyword(), one.SimpleDescription())
	}
	b.WriteString("\nMode flags:\n")
	for _, one := range u.sublaunchers {
		if one == nil {
			continue
		}
		fmt.Fprintf(&b, "  [%s]\n%s\n", one.Keyword(), one.CommandLineSyntax())
	}
	return b.String()
}

func ErrorOnUnparsedArgs(args []string) error {
	if len(args) == 0 {
		return nil
	}
	return fmt.Errorf("launcher: unparsed args: %v", args)
}
