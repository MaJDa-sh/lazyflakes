// Package options reads the option lists NixOS, home-manager and nix-darwin
// publish as JSON (built by nixpkgs' nixosOptionsDoc) and turns what a user
// types for an option into a Nix expression.
package options

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Kind is how a value for an option is entered. The JSON only carries a
// human-readable type description, so this is read from that text; anything
// not recognised is Other and takes a raw Nix expression.
type Kind int

const (
	Other       Kind = iota // a raw Nix expression
	Bool                    // true or false
	Enum                    // one of a fixed set of strings
	Int                     // an integer
	String                  // text
	Path                    // an absolute path, written as a string
	StringList              // a list of strings
	Package                 // a package, entered as its name in pkgs
	PackageList             // a list of packages
)

func (k Kind) String() string {
	return [...]string{"expression", "boolean", "choice", "integer", "string", "path", "list of strings", "package", "list of packages"}[k]
}

// Option is one documented option.
type Option struct {
	Name        string // e.g. services.openssh.enable
	Loc         []string
	Type        string // the type as described in the manual
	Kind        Kind
	Nullable    bool     // "null or …": null is allowed
	Choices     []string // for Enum
	Default     string   // Nix expression text, "" when there is none
	Example     string
	Description string // plain text
	Declared    string // the file that declares it
	ReadOnly    bool
	Templated   bool // the name has a <name> or * part, which has to be chosen first
}

// Settable reports whether a user can set the option directly: it is not
// read-only, does not need a name chosen first, and is not one of the
// module system's own (_module.args and friends).
func (o Option) Settable() bool {
	return !o.ReadOnly && !o.Templated && !strings.HasPrefix(o.Name, "_")
}

type rawOption struct {
	Type         string            `json:"type"`
	Default      json.RawMessage   `json:"default"`
	Example      json.RawMessage   `json:"example"`
	Description  *string           `json:"description"`
	Loc          []string          `json:"loc"`
	ReadOnly     bool              `json:"readOnly"`
	Declarations []json.RawMessage `json:"declarations"`
}

// Parse reads an options.json and returns its options sorted by name.
func Parse(r io.Reader) ([]Option, error) {
	var raw map[string]rawOption
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("reading options.json: %w", err)
	}
	opts := make([]Option, 0, len(raw))
	for name, ro := range raw {
		o := Option{
			Name:        name,
			Loc:         ro.Loc,
			Type:        ro.Type,
			Default:     literal(ro.Default),
			Example:     literal(ro.Example),
			Description: plainText(deref(ro.Description)),
			ReadOnly:    ro.ReadOnly,
			Templated:   strings.ContainsAny(name, "<*"),
		}
		if len(ro.Declarations) > 0 {
			o.Declared = declaration(ro.Declarations[0])
		}
		o.Kind, o.Nullable, o.Choices = classify(ro.Type)
		opts = append(opts, o)
	}
	sort.Slice(opts, func(i, j int) bool { return opts[i].Name < opts[j].Name })
	return opts, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// literal renders a default or example, which is either a
// {"_type": "literalExpression", "text": …} object or a plain JSON value.
func literal(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var lit struct {
		Type string `json:"_type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &lit) == nil && lit.Type != "" {
		return lit.Text
	}
	return string(raw)
}

// declaration is the file name from a declarations entry, which is a plain
// string or a {"name": …, "url": …} object depending on the producer.
func declaration(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Name
	}
	return ""
}

var (
	roleRe     = regexp.MustCompile("\\{[a-z]+\\}`([^`]*)`")
	choiceRe   = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	intRe      = regexp.MustCompile(`^(?:\d+ bit )?(?:signed |unsigned |positive )?integer\b|^integer between\b`)
	stringRe   = regexp.MustCompile(`^(?:non-empty |single-line )?strings?\b`)
	blankRunRe = regexp.MustCompile(`\n{3,}`)
)

// plainText strips the markdown roles and code ticks of option descriptions.
func plainText(md string) string {
	md = roleRe.ReplaceAllString(md, "$1")
	md = strings.ReplaceAll(md, "`", "")
	md = blankRunRe.ReplaceAllString(md, "\n\n")
	return strings.TrimSpace(md)
}

// classify reads the type description of an option.
func classify(typ string) (kind Kind, nullable bool, choices []string) {
	t := typ
	if rest, ok := strings.CutPrefix(t, "null or "); ok {
		t, nullable = rest, true
	}
	switch {
	case t == "boolean":
		return Bool, nullable, nil
	case strings.HasPrefix(t, "one of "):
		if c, ok := parseChoices(strings.TrimPrefix(t, "one of ")); ok {
			return Enum, nullable, c
		}
		return Other, nullable, nil
	case strings.HasPrefix(t, "string matching the pattern"):
		return String, nullable, nil
	case strings.Contains(t, " or "):
		return Other, nullable, nil // a union such as "string or signed integer"
	case intRe.MatchString(t):
		return Int, nullable, nil
	case stringRe.MatchString(t):
		return String, nullable, nil
	case strings.HasPrefix(t, "absolute path"):
		return Path, nullable, nil
	case t == "list of string":
		return StringList, nullable, nil
	case t == "package":
		return Package, nullable, nil
	case t == "list of package":
		return PackageList, nullable, nil
	}
	return Other, nullable, nil
}

// parseChoices reads `"a", "b", "c"`; any other text makes it unparseable.
func parseChoices(s string) ([]string, bool) {
	matches := choiceRe.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return nil, false
	}
	rest := choiceRe.ReplaceAllString(s, "")
	if strings.Trim(rest, ", ") != "" && strings.TrimSpace(strings.ReplaceAll(rest, ",", "")) != "or" {
		return nil, false
	}
	choices := make([]string, len(matches))
	for i, m := range matches {
		unquoted, err := strconv.Unquote(`"` + m[1] + `"`)
		if err != nil {
			unquoted = m[1]
		}
		choices[i] = unquoted
	}
	return choices, true
}
