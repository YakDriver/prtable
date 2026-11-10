package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const testRepo = "hashicorp/terraform-provider-aws"

// testRules mirrors a Terraform provider maintainer's config.
func testRules(t *testing.T, edit func(*Config)) *rules {
	t.Helper()
	cfg := defaultConfig()
	cfg.Wiki.URL = "https://github.com/o/r.wiki.git"
	cfg.Display.DefaultRepo = testRepo
	cfg.Display.JogStrip = `(?i)^(?:[rd]/|new (?:resource|data source):\s*)`
	cfg.Detect.AcceptanceOutput = []string{`--- PASS: TestAcc`, `(?s)(?:TF_ACC=1|make testacc|make t ).*\bPASS\b`}
	if edit != nil {
		edit(cfg)
	}
	r, err := cfg.compile()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func pr(body string) *pullRequest {
	p := &pullRequest{Body: body}
	p.Repository.NameWithOwner = testRepo
	p.Author.Login = "me"
	return p
}

func TestStarterMatchesDefaults(t *testing.T) {
	got, err := decodeConfig("starter.hcl", starter)
	if err != nil {
		t.Fatal(err)
	}
	want := defaultConfig()
	want.Wiki.URL = placeholderURL
	if !reflect.DeepEqual(got, want) {
		t.Errorf("starter.hcl drifted from defaultConfig:\n got %+v\nwant %+v", *got.Detect, *want.Detect)
	}
	if _, err := got.compile(); err == nil || !strings.Contains(err.Error(), "wiki.url is required") {
		t.Errorf("placeholder URL should fail validation, got %v", err)
	}
}

func TestDecodeConfigKeepsDefaults(t *testing.T) {
	src := `
wiki {
  url         = "https://github.com/o/r.wiki.git"
  merged_page = "Done.md"
}
search {
  exclude_repos = ["me/*"]
}
detect {
  copilot_verdict {
    match  = "LGTM"
    symbol = "👍"
  }
}
`
	cfg, err := decodeConfig("x.hcl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Wiki.OpenPage != "Home.md" || cfg.Wiki.MergedPage != "Done.md" {
		t.Errorf("pages = %q, %q", cfg.Wiki.OpenPage, cfg.Wiki.MergedPage)
	}
	if !slices.Equal(cfg.Search.Roles, []string{"author", "assignee"}) {
		t.Errorf("roles default lost: %v", cfg.Search.Roles)
	}
	if len(cfg.Detect.CopilotVerdicts) != 1 || cfg.Detect.CopilotLogin != "copilot" {
		t.Errorf("verdict blocks should replace defaults, other fields keep them: %+v", cfg.Detect)
	}
	if _, err := cfg.compile(); err != nil {
		t.Error(err)
	}
}

func TestDecodeConfigUnknownKey(t *testing.T) {
	_, err := decodeConfig("x.hcl", []byte("wiki {\n  ulr = \"x\"\n}\n"))
	if err == nil || !strings.Contains(err.Error(), `"ulr"`) {
		t.Errorf("want unknown-argument error naming ulr, got %v", err)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	_, err := loadConfig(filepath.Join(t.TempDir(), "none.hcl"))
	if err == nil || !strings.Contains(err.Error(), "prtable -init") {
		t.Errorf("want hint to run -init, got %v", err)
	}
}

func TestWriteStarter(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.hcl")
	if err := writeStarter(p); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(p); err != nil {
		t.Error(err)
	}
	if err := writeStarter(p); err == nil {
		t.Error("writeStarter should refuse to overwrite")
	}
}

func TestConfigPath(t *testing.T) {
	t.Setenv("PRTABLE_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	t.Chdir(t.TempDir())
	if p, _ := configPath("flag.hcl"); p != "flag.hcl" {
		t.Errorf("flag: got %s", p)
	}
	if p, _ := configPath(""); p != "/xdg/prtable/config.hcl" {
		t.Errorf("xdg: got %s", p)
	}
	if err := os.WriteFile("prtable.hcl", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if p, _ := configPath(""); p != "prtable.hcl" {
		t.Errorf("cwd: got %s", p)
	}
	t.Setenv("PRTABLE_CONFIG", "env.hcl")
	if p, _ := configPath(""); p != "env.hcl" {
		t.Errorf("env: got %s", p)
	}
}

func TestCompileValidation(t *testing.T) {
	cfg := defaultConfig()
	cfg.Wiki.MergedPage = "Home.md"
	cfg.Wiki.OpenHeading = "PRs"
	cfg.Search.Roles = []string{"owner"}
	cfg.Search.ExcludeRepos = []string{"["}
	cfg.Display.ClosedUnmerged = "keep"
	cfg.Display.Timezone = "Mars/Olympus"
	cfg.Detect.CopilotLogin = ""
	cfg.Detect.UnitOutput = []string{"("}
	cfg.Detect.DependsOn = []string{`stacks on #\d+`}
	_, err := cfg.compile()
	if err == nil {
		t.Fatal("expected validation errors")
	}
	for _, want := range []string{
		"wiki.url is required", "must differ", "open_heading", `"owner"`, `bad repo glob "["`,
		"closed_unmerged", "display.timezone", "copilot_login", "detect.unit_output", "capture group",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}

func TestWantRepo(t *testing.T) {
	r := testRules(t, func(c *Config) {
		c.Search.IncludeRepos = []string{"hashicorp/*"}
		c.Search.ExcludeRepos = []string{"hashicorp/terraform-provider-aws-private"}
	})
	cases := []struct {
		repo    string
		private bool
		want    bool
	}{
		{"hashicorp/terraform-provider-aws", false, true},
		{"HashiCorp/Terraform-Plugin-Testing", false, true},
		{"YakDriver/oschmod", false, false},
		{"hashicorp/terraform-provider-aws-private", false, false},
		{"hashicorp/internal", true, false},
	}
	for _, c := range cases {
		if got := r.wantRepo(c.repo, c.private); got != c.want {
			t.Errorf("wantRepo(%s, %v) = %v", c.repo, c.private, got)
		}
	}
	all := testRules(t, func(c *Config) { c.Search.IncludePrivate = true })
	if !all.wantRepo("anyone/secret", true) {
		t.Error("include_private with no include list should allow any repo")
	}
}

func TestSplitCells(t *testing.T) {
	got := splitCells(`| a \| b | c |  d |`)
	want := []string{`a \| b`, "c", "d"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParsePRCell(t *testing.T) {
	url, jog, draft, err := parsePRCell("List: 5 EC2 VPC [#50600](https://github.com/hashicorp/terraform-provider-aws/pull/50600) 🚧")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://github.com/hashicorp/terraform-provider-aws/pull/50600" || jog != "List: 5 EC2 VPC" || !draft {
		t.Errorf("got %q %q %v", url, jog, draft)
	}
	if _, _, _, err := parsePRCell("no link here"); err == nil {
		t.Error("expected error for cell without PR link")
	}
}

func TestOpenRoundTrip(t *testing.T) {
	r := testRules(t, nil)
	x := &row{
		URL: "https://github.com/hashicorp/terraform-plugin-testing/pull/570", Jog: `ActionChecks \| tests (terraform-plugin-testing)`,
		Draft: true, Closes: "—", DependsOn: "—", Desc: "✅", Tests: "❌", Review: "❌ 👤", FixUp: "❌ 11",
	}
	stamp := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	block := r.renderOpen([]*row{x}, stamp)
	rows, err := parseOpenRows(block)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || *rows[0] != *x {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", rows[0], x)
	}
	if !parseUpdated(block).Equal(stamp) {
		t.Errorf("updated = %v, want %v", parseUpdated(block), stamp)
	}
}

func TestLegend(t *testing.T) {
	r := testRules(t, func(c *Config) { c.Display.LegendNote = "(note here)" })
	l := r.legend()
	for _, want := range []string{"non-default repo or community author", "(🟢 approve · 🟡 changes · 🔵 closer look)", "no threads. (note here) ·"} {
		if !strings.Contains(l, want) {
			t.Errorf("legend missing %q:\n%s", want, l)
		}
	}
	bare := testRules(t, func(c *Config) {
		c.Display.DefaultRepo, c.Display.OtherAuthorTag = "", ""
		c.Detect.CopilotVerdicts = nil
	})
	l = bare.legend()
	if !strings.Contains(l, "; repo tagged in parens") || strings.Contains(l, "Copilot reviewed (") {
		t.Errorf("unexpected bare legend:\n%s", l)
	}
}

func TestRenderDoneClosedNote(t *testing.T) {
	if strings.Contains(testRules(t, nil).renderDone(nil), closedMark) {
		t.Error("drop mode shouldn't explain ✖️")
	}
	rec := testRules(t, func(c *Config) { c.Display.ClosedUnmerged = "record" })
	if !strings.Contains(rec.renderDone(nil), closedMark+" = closed without merging") {
		t.Error("record mode should explain ✖️")
	}
}

func TestClosesCell(t *testing.T) {
	body := "<!-- Closes #0000 -->\nCloses #50651\nRelates #50650\nFixes other/repo#7\ncloses #50651"
	got := closesCell(pr(body))
	want := "[#50651](https://github.com/hashicorp/terraform-provider-aws/issues/50651), [other/repo#7](https://github.com/other/repo/issues/7)"
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
	if got := closesCell(pr("fixes drift after converting")); got != "—" {
		t.Errorf("prose should not match, got %s", got)
	}
}

func TestTestsCell(t *testing.T) {
	r := testRules(t, nil)
	cases := map[string]string{
		"--- PASS: TestAccSageMakerModel_basic (22.01s)":                        "✅",
		"% make testacc TESTS=TestAccX PKG=sagemaker\n\nPASS\n":                 "✅",
		"% go test ./internal/service/sqs/...\nok  github.com/hashicorp/x\n":    "🧪",
		"`_List_*` acceptance tests pass for all six.":                          "⚠️",
		"### Output from Acceptance Testing\n\n":                                "❌",
		"<!-- --- PASS: TestAccExample -->\n### Output from Acceptance Testing": "❌",
	}
	for body, want := range cases {
		if got := r.testsCell(pr(body)); got != want {
			t.Errorf("%q: got %s, want %s", body, got, want)
		}
	}
	// Without acceptance patterns, acceptance output counts as unit output.
	generic := testRules(t, func(c *Config) { c.Detect.AcceptanceOutput = nil })
	if got := generic.testsCell(pr("--- PASS: TestAccX (1s)")); got != "🧪" {
		t.Errorf("generic config: got %s, want 🧪", got)
	}
}

func TestReviewCell(t *testing.T) {
	r := testRules(t, nil)
	p := pr("")
	add := func(login, body string) {
		p.Reviews.Nodes = append(p.Reviews.Nodes, struct {
			Author actor
			Body   string
		}{actor{login}, body})
	}
	if got := r.reviewCell(p); got != "❌" {
		t.Errorf("no reviews: got %s", got)
	}
	add("copilot-pull-request-reviewer", "### 🟡 Changes recommended")
	add("me", "self reply")    // author replies don't count as human review
	add("github-actions", "")  // ignored by config
	add("dependabot[bot]", "") // bots are always ignored
	if got := r.reviewCell(p); got != "✅ 🟡" {
		t.Errorf("got %s, want ✅ 🟡", got)
	}
	add("copilot-pull-request-reviewer", "### 🟢 Approval recommended") // latest wins
	add("gdavison", "")
	if got := r.reviewCell(p); got != "✅ 🟢 👤" {
		t.Errorf("got %s, want ✅ 🟢 👤", got)
	}
}

func TestFixUpCell(t *testing.T) {
	p := pr("")
	if got := fixUpCell(p); got != "—" {
		t.Errorf("no threads: got %s", got)
	}
	p.ReviewThreads.TotalCount = 2
	p.ReviewThreads.Nodes = []struct{ IsResolved bool }{{true}, {false}}
	if got := fixUpCell(p); got != "❌ 1" {
		t.Errorf("got %s, want ❌ 1", got)
	}
	p.ReviewThreads.Nodes[1].IsResolved = true
	if got := fixUpCell(p); got != "✅" {
		t.Errorf("got %s, want ✅", got)
	}
}

func TestJogFor(t *testing.T) {
	r := testRules(t, nil)
	p := pr("")
	p.Title = "d/`aws_sagemaker_hub_content`: New data source"
	if got := r.jogFor(p, "me"); got != "aws_sagemaker_hub_content: New data source" {
		t.Errorf("got %q", got)
	}
	p.Title = "34787 Make sqs wait times configurable for local dev speed"
	p.Repository.NameWithOwner = "hashicorp/terraform-plugin-testing"
	p.Author.Login = "someone"
	if got := r.jogFor(p, "me"); got != "34787 Make sqs wait times configurable… (terraform-plugin-testing, community)" {
		t.Errorf("got %q", got)
	}
	plain := testRules(t, func(c *Config) { c.Display.JogMaxWords, c.Display.OtherAuthorTag, c.Display.JogStrip = 0, "", "" })
	if got := plain.jogFor(p, "me"); got != "34787 Make sqs wait times configurable for local dev speed (terraform-plugin-testing)" {
		t.Errorf("got %q", got)
	}
}

func TestGuessDependsOn(t *testing.T) {
	r := testRules(t, nil)
	got := r.guessDependsOn(pr("Stacks on #50577. Part of #50576. This PR is based on #50577."))
	if got != "[#50577](https://github.com/hashicorp/terraform-provider-aws/pull/50577)" {
		t.Errorf("got %s", got)
	}
	none := testRules(t, func(c *Config) { c.Detect.DependsOn = nil })
	if got := none.guessDependsOn(pr("Stacks on #50577.")); got != "—" {
		t.Errorf("no patterns: got %s", got)
	}
}

func TestInsertRow(t *testing.T) {
	base := "https://github.com/o/r/pull/1"
	rows := []*row{
		{URL: base, DependsOn: "—"},
		{URL: "https://github.com/o/r/pull/2", DependsOn: "[#1](" + base + ")"},
		{URL: "https://github.com/o/r/pull/9", DependsOn: "—"},
	}
	x := &row{URL: "https://github.com/o/r/pull/3", DependsOn: "[#1](" + base + ")"}
	got := insertRow(rows, x)
	if got[2] != x {
		t.Errorf("dependent should follow base's last dependent, got order %v", urls(got))
	}
	loose := &row{URL: "https://github.com/o/r/pull/4", DependsOn: "—"}
	if got = insertRow(got, loose); got[len(got)-1] != loose {
		t.Errorf("independent row should append, got order %v", urls(got))
	}
}

func urls(rows []*row) []string {
	var out []string
	for _, x := range rows {
		out = append(out, x.URL[strings.LastIndex(x.URL, "/")+1:])
	}
	return out
}

func TestStrikeMerged(t *testing.T) {
	s := &syncer{gh: newGitHub(), r: testRules(t, nil)}
	merged := "https://github.com/o/r/pull/1"
	open := "https://github.com/o/r/pull/2"
	s.gh.cache[merged] = &pullRequest{URL: merged, State: "MERGED"}
	s.gh.cache[open] = &pullRequest{URL: open, State: "OPEN"}
	cell := "[#1](" + merged + ") (rel), [#2](" + open + "), ~~[#3](https://github.com/o/r/pull/3)~~"
	got := s.strikeMerged(cell)
	want := "~~[#1](" + merged + ")~~ (rel), [#2](" + open + "), ~~[#3](https://github.com/o/r/pull/3)~~"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestEnsureOpenBlock(t *testing.T) {
	h := "## PRs in process"
	doc := "# Title\n\n" + h + "\n\nIntro.\n\n| PR | x |\n|--|--|\n| a | b |\n\nLegend — stuff\n\n## Today\n"
	got := ensureOpenBlock(doc, h)
	want := "# Title\n\n" + h + "\n\nIntro.\n\n" + openStart + "\n| PR | x |\n|--|--|\n| a | b |\n\nLegend — stuff\n" + openEnd + "\n\n## Today\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if ensureOpenBlock(got, h) != got {
		t.Error("ensureOpenBlock is not idempotent")
	}
	if got := ensureOpenBlock("", "## Mine"); got != "## Mine\n\n"+openStart+"\n"+openEnd+"\n" {
		t.Errorf("new page: got %q", got)
	}
	if got := ensureOpenBlock("Existing text\n", "## Mine"); !strings.HasPrefix(got, "## Mine\n\n"+openStart) || !strings.HasSuffix(got, "\n\nExisting text\n") {
		t.Errorf("no heading: got %q", got)
	}
	if got := ensureOpenBlock("x\n## Mine\n\n## Next\n", "## Mine"); !strings.Contains(got, "## Mine\n\n"+openStart+"\n"+openEnd+"\n\n## Next") {
		t.Errorf("heading without table: got %q", got)
	}
}

func TestEnsureDoneBlock(t *testing.T) {
	got := ensureDoneBlock("* old item\n", "## Merged PRs", "## Other completed")
	if !strings.HasPrefix(got, "## Merged PRs\n\n"+doneStart+"\n"+doneEnd) || !strings.HasSuffix(got, "## Other completed\n\n* old item\n") {
		t.Errorf("unexpected:\n%s", got)
	}
	if ensureDoneBlock(got, "## Merged PRs", "## Other completed") != got {
		t.Error("ensureDoneBlock is not idempotent")
	}
	if got := ensureDoneBlock("", "## Done", "## Other"); strings.Contains(got, "## Other") {
		t.Errorf("empty page shouldn't get other heading: %q", got)
	}
	if got := ensureDoneBlock("* x\n", "## Done", ""); !strings.HasSuffix(got, doneEnd+"\n\n* x\n") {
		t.Errorf("no other heading: %q", got)
	}
}

func TestIssuesConfig(t *testing.T) {
	r := testRules(t, func(c *Config) {
		c.Issues.AssignedRepos = []string{"hashicorp/*"}
		c.Issues.AllRepos = []string{"YakDriver/gh-averyindex"}
	})
	cases := []struct {
		repo     string
		assigned bool
		want     bool
	}{
		{"hashicorp/terraform-provider-aws", true, true},
		{"hashicorp/terraform-provider-aws", false, false},
		{"yakdriver/GH-AveryIndex", false, true},
		{"YakDriver/oschmod", true, false},
	}
	for _, c := range cases {
		if got := r.wantIssue(c.repo, c.assigned); got != c.want {
			t.Errorf("wantIssue(%s, %v) = %v", c.repo, c.assigned, got)
		}
	}
	if testRules(t, nil).cfg.Issues.enabled() {
		t.Error("issues should be off by default")
	}

	cfg := defaultConfig()
	cfg.Wiki.URL = "https://github.com/o/r.wiki.git"
	cfg.Issues.AssignedRepos = []string{"["}
	cfg.Issues.AllRepos = []string{"hashicorp/*", "noslash"}
	cfg.Issues.Heading = cfg.Wiki.OpenHeading
	_, err := cfg.compile()
	for _, want := range []string{`issues.assigned_repos: bad repo glob "["`, `"hashicorp/*" must be an exact`, `"noslash" must be an exact`, "must differ"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}

func testIssue(url, repo string, assignees ...string) *issue {
	i := &issue{URL: url, Title: "Cache API responses for a short time"}
	i.Repository.NameWithOwner = repo
	for _, a := range assignees {
		i.Assignees.Nodes = append(i.Assignees.Nodes, actor{a})
	}
	return i
}

func TestIssueCells(t *testing.T) {
	i := testIssue("https://github.com/o/r/issues/1", "o/r", "me", "you")
	if got := assigneesCell(i); got != "me, you" {
		t.Errorf("assignees: got %s", got)
	}
	if got := assigneesCell(testIssue("u", "o/r")); got != "—" {
		t.Errorf("no assignees: got %s", got)
	}
	if got := issuePRsCell(i); got != "—" {
		t.Errorf("no PRs: got %s", got)
	}
	type ref = struct {
		Number     int
		URL        string
		Repository struct{ NameWithOwner string }
	}
	a, b := ref{Number: 5, URL: "https://github.com/o/r/pull/5"}, ref{Number: 7, URL: "https://github.com/x/y/pull/7"}
	a.Repository.NameWithOwner, b.Repository.NameWithOwner = "o/r", "x/y"
	i.ClosedByPullRequestsReferences.Nodes = []ref{a, b}
	if got := issuePRsCell(i); got != "[#5](https://github.com/o/r/pull/5), [x/y#7](https://github.com/x/y/pull/7)" {
		t.Errorf("PRs: got %s", got)
	}
}

func TestIssuesRoundTrip(t *testing.T) {
	r := testRules(t, nil)
	x := &issueRow{URL: "https://github.com/YakDriver/gh-averyindex/issues/12", Jog: `Cache \| API (gh-averyindex)`, Assignees: "—", PRs: "[#14](https://github.com/YakDriver/gh-averyindex/pull/14)"}
	rows, err := parseIssueRows(r.renderIssues([]*issueRow{x}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || *rows[0] != *x {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", rows, x)
	}
	if _, err := parseIssueRows("| PR link [#1](https://github.com/o/r/pull/1) | a | b |"); err == nil {
		t.Error("a PR link isn't an issue cell")
	}
}

func TestEnsureIssuesBlock(t *testing.T) {
	doc := "## PRs in process\n\n" + openStart + "\nx\n" + openEnd + "\n\n## Today\n"
	got := ensureIssuesBlock(doc, "## Issues")
	want := "## PRs in process\n\n" + openStart + "\nx\n" + openEnd + "\n\n## Issues\n\n" + issuesStart + "\n" + issuesEnd + "\n\n## Today\n"
	if got != want {
		t.Errorf("after open table:\ngot  %q\nwant %q", got, want)
	}
	if ensureIssuesBlock(got, "## Issues") != got {
		t.Error("ensureIssuesBlock is not idempotent")
	}
	withHeading := "## Issues\n\nnotes\n\n" + doc
	if got := ensureIssuesBlock(withHeading, "## Issues"); !strings.HasPrefix(got, "## Issues\n\n"+issuesStart+"\n"+issuesEnd+"\n\nnotes") {
		t.Errorf("existing heading: got %q", got)
	}
}

func TestSyncIssues(t *testing.T) {
	r := testRules(t, func(c *Config) {
		c.Issues.AssignedRepos = []string{"hashicorp/*"}
		c.Issues.AllRepos = []string{"YakDriver/gh-averyindex"}
	})
	keep := "https://github.com/YakDriver/gh-averyindex/issues/12"
	gone := "https://github.com/YakDriver/gh-averyindex/issues/9"
	added := "https://github.com/YakDriver/gh-averyindex/issues/13"
	mine := "https://github.com/hashicorp/terraform-provider-aws/issues/50581"
	secret := testIssue("https://github.com/hashicorp/secret/issues/1", "hashicorp/secret", "me")
	secret.Repository.IsPrivate = true
	var queries []string
	s := &syncer{r: r, me: "me", issueSearch: func(q string) ([]*issue, error) {
		queries = append(queries, q)
		if strings.Contains(q, "assignee:me") {
			// The assignee search spans every repo; only assigned_repos count.
			return []*issue{
				testIssue(mine, testRepo, "me"),
				testIssue("https://github.com/YakDriver/oschmod/issues/3", "YakDriver/oschmod", "me"),
				secret,
				testIssue(keep, "YakDriver/gh-averyindex", "me"),
			}, nil
		}
		return []*issue{testIssue(keep, "YakDriver/gh-averyindex", "me"), testIssue(added, "YakDriver/gh-averyindex")}, nil
	}}
	rows := []*issueRow{{URL: gone, Jog: "old"}, {URL: keep, Jog: "mine", Assignees: "—", PRs: "—"}}
	got, err := s.syncIssues(rows)
	if err != nil {
		t.Fatal(err)
	}
	wantQ := []string{
		"is:issue is:open archived:false assignee:me sort:created-asc",
		"is:issue is:open archived:false repo:YakDriver/gh-averyindex sort:created-asc",
	}
	if !slices.Equal(queries, wantQ) {
		t.Errorf("queries:\n got %q\nwant %q", queries, wantQ)
	}
	var gotURLs []string
	for _, x := range got {
		gotURLs = append(gotURLs, x.URL)
	}
	if want := []string{keep, mine, added}; !slices.Equal(gotURLs, want) {
		t.Fatalf("rows:\n got %v\nwant %v", gotURLs, want)
	}
	if got[0].Jog != "mine" || got[0].Assignees != "me" {
		t.Errorf("existing row should keep its jog and refresh cells: %+v", got[0])
	}
	if got[1].Jog != "Cache API responses for a short…" {
		t.Errorf("default-repo jog: got %q", got[1].Jog)
	}
	if got[2].Jog != "Cache API responses for a short… (gh-averyindex)" {
		t.Errorf("new jog: got %q", got[2].Jog)
	}
	notes := strings.Join(s.notes, "\n")
	for _, want := range []string{"dropped " + gone, "skipped 1 issue(s) in private hashicorp/secret"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes missing %q:\n%s", want, notes)
		}
	}
}
