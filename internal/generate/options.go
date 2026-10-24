package generate

import (
	"fmt"
	"strings"
)

// Option is a nixpkgs config setting that can be enabled for a shell.
type Option int

const (
	Unfree Option = iota
	Insecure
	Broken
	Unsupported
)

// AllOptions lists every option in menu order.
var AllOptions = []Option{Unfree, Insecure, Broken, Unsupported}

var optionInfo = [...]struct {
	key, name, desc, attr, value, env string
}{
	Unfree:      {"unfree", "allow unfree", "proprietary licenses, e.g. vscode, steam, cuda", "allowUnfree", "true", "NIXPKGS_ALLOW_UNFREE=1"},
	Insecure:    {"insecure", "allow insecure", "packages with known vulnerabilities", "allowInsecurePredicate", "_: true", "NIXPKGS_ALLOW_INSECURE=1"},
	Broken:      {"broken", "allow broken", "packages marked as broken", "allowBroken", "true", "NIXPKGS_ALLOW_BROKEN=1"},
	Unsupported: {"unsupported", "allow unsupported", "packages not marked for this system", "allowUnsupportedSystem", "true", "NIXPKGS_ALLOW_UNSUPPORTED_SYSTEM=1"},
}

// Key is the short name used by -allow, e.g. "unfree".
func (o Option) Key() string         { return optionInfo[o].key }
func (o Option) Name() string        { return optionInfo[o].name }
func (o Option) Description() string { return optionInfo[o].desc }

// Setting is the nixpkgs config attribute and the Nix value that enable it.
func (o Option) Setting() (attr, value string) { return optionInfo[o].attr, optionInfo[o].value }

// Enabled returns the enabled options in menu order.
func (s Options) Enabled() []Option { return s.enabled() }

// Options is the set of enabled options.
type Options uint

func (s Options) Has(o Option) bool       { return s&(1<<o) != 0 }
func (s Options) Toggle(o Option) Options { return s ^ (1 << o) }
func (s Options) With(o Option) Options   { return s | (1 << o) }

func (s Options) enabled() []Option {
	var opts []Option
	for _, o := range AllOptions {
		if s.Has(o) {
			opts = append(opts, o)
		}
	}
	return opts
}

// Keys returns the enabled options' short names, e.g. ["unfree", "broken"].
func (s Options) Keys() []string {
	var keys []string
	for _, o := range s.enabled() {
		keys = append(keys, o.Key())
	}
	return keys
}

// Env returns NIXPKGS_ALLOW_* assignments for commands that take packages
// straight from a flake (these need --impure to be honoured).
func (s Options) Env() []string {
	var env []string
	for _, o := range s.enabled() {
		env = append(env, optionInfo[o].env)
	}
	return env
}

// configExpr renders the nixpkgs config attrset, or "" when nothing is set.
func (s Options) configExpr() string {
	var attrs []string
	for _, o := range s.enabled() {
		attr, value := o.Setting()
		attrs = append(attrs, attr+" = "+value+";")
	}
	if len(attrs) == 0 {
		return ""
	}
	return "{ " + strings.Join(attrs, " ") + " }"
}

// ParseOptions reads a comma-separated list of option keys.
func ParseOptions(list string) (Options, error) {
	var s Options
	for _, key := range strings.Split(list, ",") {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		found := false
		for _, o := range AllOptions {
			if o.Key() == key {
				s, found = s.With(o), true
			}
		}
		if !found {
			return 0, fmt.Errorf("unknown option %q (want unfree, insecure, broken or unsupported)", key)
		}
	}
	return s, nil
}
