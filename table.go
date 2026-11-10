package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	openStart = "<!-- prtable:open:start -->"
	openEnd   = "<!-- prtable:open:end -->"
	doneStart = "<!-- prtable:merged:start -->"
	doneEnd   = "<!-- prtable:merged:end -->"

	issuesStart = "<!-- prtable:issues:start -->"
	issuesEnd   = "<!-- prtable:issues:end -->"

	openHeader = "| PR | Closes | Depends On | Desc | Tests | Review | Fix Up |\n" +
		"|----|:------:|:----------:|:----:|:-----:|:------:|:------:|"
	doneHeader = "| PR | Closes | Merged |\n" +
		"|----|:------:|:------:|"
	issuesHeader = "| Issue | Assignees | PRs |\n" +
		"|-------|:---------:|:---:|"

	closedMark = "✖️"
)

// row is one PR in either table. Jog, Desc, and DependsOn are hand-owned and
// survive regeneration; the other fields are recomputed from GitHub.
type row struct {
	URL       string
	Jog       string
	Draft     bool
	Closes    string
	DependsOn string
	Desc      string
	Tests     string
	Review    string
	FixUp     string
	Merged    string // merged table only: YYYY-MM-DD, or ✖️ YYYY-MM-DD if closed unmerged
}

var (
	rePRCell    = regexp.MustCompile(`^(.*?)\s*\[#\d+\]\((https://github\.com/[^)\s]+/pull/\d+)\)\s*(🚧)?\s*$`)
	reIssueCell = regexp.MustCompile(`^(.*?)\s*\[#\d+\]\((https://github\.com/[^)\s]+/issues/\d+)\)\s*$`)
	reUpdated   = regexp.MustCompile(`<!-- prtable:updated (\S+) -->`)
)

// issueRow is one issue in the issues table. Jog is hand-owned; the other
// fields are recomputed from GitHub.
type issueRow struct {
	URL       string
	Jog       string
	Assignees string
	PRs       string
}

// splitCells splits a markdown table line on unescaped pipes.
func splitCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, `\|`) {
		line = line[:len(line)-1]
	}
	var cells []string
	var b strings.Builder
	prev := rune(0)
	for _, r := range line {
		if r == '|' && prev != '\\' {
			cells = append(cells, strings.TrimSpace(b.String()))
			b.Reset()
		} else {
			b.WriteRune(r)
		}
		prev = r
	}
	return append(cells, strings.TrimSpace(b.String()))
}

// dataRows returns the cells of each table body row in block, skipping the
// header and separator lines.
func dataRows(block string) [][]string {
	var out [][]string
	for l := range strings.SplitSeq(block, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "|") || strings.HasPrefix(l, "|-") {
			continue
		}
		cells := splitCells(l)
		if cells[0] == "PR" || cells[0] == "Issue" {
			continue
		}
		out = append(out, cells)
	}
	return out
}

func parsePRCell(cell string) (url, jog string, draft bool, err error) {
	m := rePRCell.FindStringSubmatch(cell)
	if m == nil {
		return "", "", false, fmt.Errorf("unrecognized PR cell %q", cell)
	}
	return m[2], strings.TrimSpace(m[1]), m[3] != "", nil
}

func parseOpenRows(block string) ([]*row, error) {
	var rows []*row
	for _, c := range dataRows(block) {
		if len(c) < 7 {
			return nil, fmt.Errorf("open table row has %d cells, want 7: %q", len(c), c)
		}
		url, jog, draft, err := parsePRCell(c[0])
		if err != nil {
			return nil, err
		}
		rows = append(rows, &row{
			URL: url, Jog: jog, Draft: draft,
			Closes: c[1], DependsOn: c[2], Desc: c[3], Tests: c[4], Review: c[5], FixUp: c[6],
		})
	}
	return rows, nil
}

func parseDoneRows(block string) ([]*row, error) {
	var rows []*row
	for _, c := range dataRows(block) {
		if len(c) < 3 {
			return nil, fmt.Errorf("merged table row has %d cells, want 3: %q", len(c), c)
		}
		url, jog, _, err := parsePRCell(c[0])
		if err != nil {
			return nil, err
		}
		rows = append(rows, &row{URL: url, Jog: jog, Closes: c[1], Merged: c[2]})
	}
	return rows, nil
}

func parseIssueRows(block string) ([]*issueRow, error) {
	var rows []*issueRow
	for _, c := range dataRows(block) {
		if len(c) < 3 {
			return nil, fmt.Errorf("issues table row has %d cells, want 3: %q", len(c), c)
		}
		m := reIssueCell.FindStringSubmatch(c[0])
		if m == nil {
			return nil, fmt.Errorf("unrecognized issue cell %q", c[0])
		}
		rows = append(rows, &issueRow{URL: m[2], Jog: strings.TrimSpace(m[1]), Assignees: c[1], PRs: c[2]})
	}
	return rows, nil
}

func parseUpdated(block string) time.Time {
	m := reUpdated.FindStringSubmatch(block)
	if m == nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, m[1])
	return t
}

func prCell(r *row, withDraft bool) string {
	n := r.URL[strings.LastIndex(r.URL, "/")+1:]
	s := fmt.Sprintf("[#%s](%s)", n, r.URL)
	if r.Jog != "" {
		s = r.Jog + " " + s
	}
	if withDraft && r.Draft {
		s += " 🚧"
	}
	return s
}

func (r *rules) legend() string {
	d := r.cfg.Display
	tags := "non-default repo"
	if d.DefaultRepo == "" {
		tags = "repo"
	}
	if d.OtherAuthorTag != "" {
		tags += " or " + d.OtherAuthorTag + " author"
	}
	review := "✅ Copilot reviewed"
	var verdicts []string
	for _, v := range r.cfg.Detect.CopilotVerdicts {
		verdicts = append(verdicts, strings.TrimSpace(v.Symbol+" "+v.Label))
	}
	if len(verdicts) > 0 {
		review += " (" + strings.Join(verdicts, " · ") + ")"
	}
	fixUp := "✅ all review threads resolved / ❌ n = n unresolved / — no threads."
	if d.LegendNote != "" {
		fixUp += " " + d.LegendNote
	}
	return "Legend — **PR**: 🚧 draft (no mark = ready); " + tags + " tagged in parens · " +
		"**Closes**: issue(s) auto-closed on merge · " +
		"**Depends On**: `(rel)` = related, not a hard block; ~~struck~~ = merged · " +
		"**Desc**: ✅ ready / ⚠️ needs work / ❔ not yet judged · " +
		"**Tests**: ✅ acc output posted / 🧪 unit output only / ⚠️ claimed, no output / ❌ none · " +
		"**Review**: " + review + " / ❌ no Copilot · 👤 human review present · " +
		"**Fix Up**: " + fixUp + " · " +
		"Memory jogger, Desc, and Depends On are yours to edit; the rest is regenerated by `prtable`."
}

func (r *rules) renderOpen(rows []*row, updated time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- prtable:updated %s -->\n", updated.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "_Updated %s by `prtable`._\n\n", updated.In(r.loc).Format("2006-01-02 15:04 MST"))
	b.WriteString(openHeader + "\n")
	for _, x := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			prCell(x, true), x.Closes, x.DependsOn, x.Desc, x.Tests, x.Review, x.FixUp)
	}
	b.WriteString("\n" + r.legend() + "\n")
	return b.String()
}

func (r *rules) renderDone(rows []*row) string {
	var b strings.Builder
	b.WriteString(doneHeader + "\n")
	for _, x := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", prCell(x, false), x.Closes, x.Merged)
	}
	if r.cfg.Display.ClosedUnmerged == "record" {
		b.WriteString("\n" + closedMark + " = closed without merging\n")
	}
	return b.String()
}

func (r *rules) renderIssues(rows []*issueRow) string {
	var b strings.Builder
	b.WriteString(issuesHeader + "\n")
	for _, x := range rows {
		n := x.URL[strings.LastIndex(x.URL, "/")+1:]
		cell := fmt.Sprintf("[#%s](%s)", n, x.URL)
		if x.Jog != "" {
			cell = x.Jog + " " + cell
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", cell, x.Assignees, x.PRs)
	}
	tags := "non-default repo"
	if r.cfg.Display.DefaultRepo == "" {
		tags = "repo"
	}
	b.WriteString("\nLegend — **Issue**: " + tags + " tagged in parens · " +
		"**Assignees**: who's assigned (— nobody) · " +
		"**PRs**: open PRs that close it on merge · " +
		"Memory jogger is yours to edit; the rest is regenerated by `prtable`.\n")
	return b.String()
}

// ensureIssuesBlock adds the issues table's markers the first time the tool
// runs with issues configured: under heading if the page has it, otherwise
// with the heading right after the open table.
func ensureIssuesBlock(doc, heading string) string {
	if strings.Contains(doc, issuesStart) {
		return doc
	}
	empty := issuesStart + "\n" + issuesEnd
	lines := strings.Split(doc, "\n")
	if h := slices.IndexFunc(lines, func(l string) bool { return strings.TrimSpace(l) == heading }); h >= 0 {
		return strings.Join(slices.Concat(lines[:h+1], []string{"", empty}, lines[h+1:]), "\n")
	}
	i := strings.Index(doc, openEnd) + len(openEnd)
	return doc[:i] + "\n\n" + heading + "\n\n" + empty + doc[i:]
}

// removeBlock deletes the markers from start through end and what's
// between them, leaving one blank line where they were. It reports whether
// the markers were there.
func removeBlock(doc, start, end string) (string, bool) {
	i := strings.Index(doc, start)
	j := strings.Index(doc, end)
	if i < 0 || j < i {
		return doc, false
	}
	before := strings.TrimRight(doc[:i], "\n")
	after := strings.TrimLeft(doc[j+len(end):], "\n")
	switch {
	case before == "":
		return after, true
	case after == "":
		return before + "\n", true
	}
	return before + "\n\n" + after, true
}

func extractBlock(doc, start, end string) (string, error) {
	i := strings.Index(doc, start)
	j := strings.Index(doc, end)
	if i < 0 || j < i {
		return "", fmt.Errorf("markers %s / %s not found", start, end)
	}
	return doc[i+len(start) : j], nil
}

func replaceBlock(doc, start, end, inner string) (string, error) {
	i := strings.Index(doc, start)
	j := strings.Index(doc, end)
	if i < 0 || j < i {
		return "", fmt.Errorf("markers %s / %s not found", start, end)
	}
	return doc[:i+len(start)] + "\n" + inner + doc[j:], nil
}

// ensureOpenBlock adds the open table's markers the first time the tool
// runs. Under heading, it adopts an existing hand-built table (and a Legend
// line after it). With no such heading, it puts the heading and an empty
// block at the top of the page.
func ensureOpenBlock(doc, heading string) string {
	if strings.Contains(doc, openStart) {
		return doc
	}
	empty := openStart + "\n" + openEnd
	lines := strings.Split(doc, "\n")
	h := slices.IndexFunc(lines, func(l string) bool { return strings.TrimSpace(l) == heading })
	if h < 0 {
		if strings.TrimSpace(doc) == "" {
			return heading + "\n\n" + empty + "\n"
		}
		return heading + "\n\n" + empty + "\n\n" + doc
	}
	isTable := func(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "|") }
	i := h + 1
	for i < len(lines) && !isTable(lines[i]) && !strings.HasPrefix(lines[i], "#") {
		i++
	}
	if i == len(lines) || !isTable(lines[i]) {
		out := slices.Concat(lines[:h+1], []string{"", openStart, openEnd}, lines[h+1:])
		return strings.Join(out, "\n")
	}
	j := i
	for j < len(lines) && isTable(lines[j]) {
		j++
	}
	k := j
	for k < len(lines) && strings.TrimSpace(lines[k]) == "" {
		k++
	}
	if k < len(lines) && strings.HasPrefix(lines[k], "Legend") {
		j = k + 1
	}
	out := slices.Concat(lines[:i], []string{openStart}, lines[i:j], []string{openEnd}, lines[j:])
	return strings.Join(out, "\n")
}

// ensureDoneBlock adds the merged table to the top of the page the first
// time the tool runs, putting any existing content under otherHeading.
func ensureDoneBlock(doc, heading, otherHeading string) string {
	if strings.Contains(doc, doneStart) {
		return doc
	}
	block := heading + "\n\n" + doneStart + "\n" + doneEnd + "\n"
	if strings.TrimSpace(doc) == "" {
		return block
	}
	if otherHeading != "" {
		return block + "\n" + otherHeading + "\n\n" + doc
	}
	return block + "\n" + doc
}
