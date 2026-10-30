package main

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2/hclsimple"
)

// Config is everything about one person's setup, read from an HCL file.
// Anything left out of the file keeps its default from defaultConfig. A
// list or repeated block in the file replaces the default rather than adding
// to it. starter.hcl documents every setting.
type Config struct {
	Wiki    *WikiConfig    `hcl:"wiki,block"`
	Search  *SearchConfig  `hcl:"search,block"`
	Display *DisplayConfig `hcl:"display,block"`
	Detect  *DetectConfig  `hcl:"detect,block"`
}

type WikiConfig struct {
	URL           string `hcl:"url,optional"`
	OpenPage      string `hcl:"open_page,optional"`
	MergedPage    string `hcl:"merged_page,optional"`
	OpenHeading   string `hcl:"open_heading,optional"`
	MergedHeading string `hcl:"merged_heading,optional"`
	OtherHeading  string `hcl:"other_heading,optional"`
	CommitMessage string `hcl:"commit_message,optional"`
}

type SearchConfig struct {
	Roles          []string `hcl:"roles,optional"`
	IncludeRepos   []string `hcl:"include_repos,optional"`
	ExcludeRepos   []string `hcl:"exclude_repos,optional"`
	IncludePrivate bool     `hcl:"include_private,optional"`
	ExtraQuery     string   `hcl:"extra_query,optional"`
}

type DisplayConfig struct {
	DefaultRepo    string `hcl:"default_repo,optional"`
	Timezone       string `hcl:"timezone,optional"`
	JogMaxWords    int    `hcl:"jog_max_words,optional"`
	JogStrip       string `hcl:"jog_strip,optional"`
	OtherAuthorTag string `hcl:"other_author_tag,optional"`
	ClosedUnmerged string `hcl:"closed_unmerged,optional"`
	LegendNote     string `hcl:"legend_note,optional"`
}

type DetectConfig struct {
	CopilotLogin     string    `hcl:"copilot_login,optional"`
	CopilotVerdicts  []Verdict `hcl:"copilot_verdict,block"`
	IgnoreReviewers  []string  `hcl:"ignore_reviewers,optional"`
	AcceptanceOutput []string  `hcl:"acceptance_output,optional"`
	UnitOutput       []string  `hcl:"unit_output,optional"`
	TestClaim        []string  `hcl:"test_claim,optional"`
	DependsOn        []string  `hcl:"depends_on,optional"`
}

type Verdict struct {
	Match  string `hcl:"match"`
	Symbol string `hcl:"symbol"`
	Label  string `hcl:"label,optional"`
}

func defaultConfig() *Config {
	return &Config{
		Wiki: &WikiConfig{
			OpenPage:      "Home.md",
			MergedPage:    "Merged-PRs.md",
			OpenHeading:   "## PRs in process",
			MergedHeading: "## Merged PRs",
			OtherHeading:  "## Other completed",
			CommitMessage: "prtable: sync PR tables",
		},
		Search: &SearchConfig{
			Roles:        []string{"author", "assignee"},
			IncludeRepos: []string{},
			ExcludeRepos: []string{},
			ExtraQuery:   "archived:false",
		},
		Display: &DisplayConfig{
			JogMaxWords:    6,
			OtherAuthorTag: "community",
			ClosedUnmerged: "drop",
		},
		Detect: &DetectConfig{
			CopilotLogin: "copilot",
			CopilotVerdicts: []Verdict{
				{Match: "Approval recommended", Symbol: "🟢", Label: "approve"},
				{Match: "Changes recommended", Symbol: "🟡", Label: "changes"},
				{Match: "Needs a closer look", Symbol: "🔵", Label: "closer look"},
			},
			IgnoreReviewers:  []string{"github-actions"},
			AcceptanceOutput: []string{},
			UnitOutput:       []string{`(?m)--- PASS: Test`, `(?m)^ok\s+\S`},
			TestClaim:        []string{`(?i)\btests?\s+pass`},
			DependsOn:        []string{`(?i)\b(?:stack(?:ed|s)?\s+on|based\s+on|depends\s+on)\s+#(\d+)\b`},
		},
	}
}

// starter is the file prtable -init writes: the defaults, commented, with a
// placeholder wiki URL. A test checks it decodes to defaultConfig.
//
//go:embed starter.hcl
var starter []byte

const placeholderURL = "https://github.com/OWNER/REPO.wiki.git"

var validRoles = []string{"author", "assignee", "review-requested", "reviewed-by", "commenter", "mentions", "involves"}

// configPath picks the config file: the -config flag, then $PRTABLE_CONFIG,
// then ./prtable.hcl, then $XDG_CONFIG_HOME/prtable/config.hcl
// (~/.config/prtable/config.hcl if XDG_CONFIG_HOME is unset).
func configPath(flagPath string) (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	if p := os.Getenv("PRTABLE_CONFIG"); p != "" {
		return p, nil
	}
	if _, err := os.Stat("prtable.hcl"); err == nil {
		return "prtable.hcl", nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "prtable", "config.hcl"), nil
}

func loadConfig(p string) (*Config, error) {
	src, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no config at %s; create one with: prtable -init", p)
	}
	if err != nil {
		return nil, err
	}
	return decodeConfig(p, src)
}

func decodeConfig(name string, src []byte) (*Config, error) {
	cfg := defaultConfig()
	// hclsimple picks the syntax from the extension, so always parse as
	// native HCL whatever the file is called.
	if !strings.HasSuffix(name, ".hcl") {
		name += ".hcl"
	}
	if err := hclsimple.Decode(name, src, nil, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func writeStarter(p string) error {
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("%s already exists", p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, starter, 0o644)
}

// rules is a validated config with its regexes compiled.
type rules struct {
	cfg                         *Config
	loc                         *time.Location
	acc, unit, claim, dependsOn []*regexp.Regexp
	jogStrip                    *regexp.Regexp
}

func (c *Config) compile() (*rules, error) {
	r := &rules{cfg: c, loc: time.Local}
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	w := c.Wiki
	if w.URL == "" || w.URL == placeholderURL {
		fail("wiki.url is required")
	}
	if w.OpenPage == "" || w.MergedPage == "" {
		fail("wiki.open_page and wiki.merged_page are required")
	} else if w.OpenPage == w.MergedPage {
		fail("wiki.open_page and wiki.merged_page must differ")
	}
	if !strings.HasPrefix(w.OpenHeading, "#") {
		fail("wiki.open_heading must be a markdown heading, got %q", w.OpenHeading)
	}
	if !strings.HasPrefix(w.MergedHeading, "#") {
		fail("wiki.merged_heading must be a markdown heading, got %q", w.MergedHeading)
	}
	if w.OtherHeading != "" && !strings.HasPrefix(w.OtherHeading, "#") {
		fail("wiki.other_heading must be empty or a markdown heading, got %q", w.OtherHeading)
	}
	if w.CommitMessage == "" {
		fail("wiki.commit_message is required")
	}

	if len(c.Search.Roles) == 0 {
		fail("search.roles needs at least one of %s", strings.Join(validRoles, ", "))
	}
	for _, role := range c.Search.Roles {
		if !slices.Contains(validRoles, role) {
			fail("search.roles: %q isn't one of %s", role, strings.Join(validRoles, ", "))
		}
	}
	for _, g := range slices.Concat(c.Search.IncludeRepos, c.Search.ExcludeRepos) {
		if _, err := path.Match(g, "owner/repo"); err != nil {
			fail("bad repo glob %q: %v", g, err)
		}
	}

	d := c.Display
	if d.Timezone != "" {
		loc, err := time.LoadLocation(d.Timezone)
		if err != nil {
			fail("display.timezone: %v", err)
		} else {
			r.loc = loc
		}
	}
	if d.JogMaxWords < 0 {
		fail("display.jog_max_words can't be negative")
	}
	if d.ClosedUnmerged != "drop" && d.ClosedUnmerged != "record" {
		fail(`display.closed_unmerged must be "drop" or "record", got %q`, d.ClosedUnmerged)
	}
	if d.JogStrip != "" {
		re, err := regexp.Compile(d.JogStrip)
		if err != nil {
			fail("display.jog_strip: %v", err)
		}
		r.jogStrip = re
	}

	if c.Detect.CopilotLogin == "" {
		fail("detect.copilot_login is required")
	}
	compileAll := func(name string, pats []string, groups int) []*regexp.Regexp {
		var out []*regexp.Regexp
		for _, p := range pats {
			re, err := regexp.Compile(p)
			switch {
			case err != nil:
				fail("detect.%s: %v", name, err)
			case re.NumSubexp() < groups:
				fail("detect.%s: %q needs a capture group for the PR number", name, p)
			default:
				out = append(out, re)
			}
		}
		return out
	}
	r.acc = compileAll("acceptance_output", c.Detect.AcceptanceOutput, 0)
	r.unit = compileAll("unit_output", c.Detect.UnitOutput, 0)
	r.claim = compileAll("test_claim", c.Detect.TestClaim, 0)
	r.dependsOn = compileAll("depends_on", c.Detect.DependsOn, 1)

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return r, nil
}

func matchAny(globs []string, repo string) bool {
	for _, g := range globs {
		if ok, _ := path.Match(strings.ToLower(g), strings.ToLower(repo)); ok {
			return true
		}
	}
	return false
}

// wantRepo reports whether PRs found in repo belong in the table.
func (r *rules) wantRepo(repo string, private bool) bool {
	s := r.cfg.Search
	switch {
	case private && !s.IncludePrivate, matchAny(s.ExcludeRepos, repo):
		return false
	case len(s.IncludeRepos) > 0:
		return matchAny(s.IncludeRepos, repo)
	}
	return true
}

func (r *rules) mergedPageName() string {
	return strings.TrimSuffix(r.cfg.Wiki.MergedPage, ".md")
}
