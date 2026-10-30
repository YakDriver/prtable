package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	reCloses      = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+([\w.-]+/[\w.-]+)?#(\d+)\b`)
	reDepLink     = regexp.MustCompile(`(~~)?(\[#\d+\]\((https://github\.com/[^)\s]+/pull/\d+)\))(~~)?`)
)

// cleanBody drops HTML comments, which in PR templates contain sample text
// like "Closes #0000".
func cleanBody(p *pullRequest) string {
	return reHTMLComment.ReplaceAllString(p.Body, "")
}

func anyMatch(res []*regexp.Regexp, s string) bool {
	return slices.ContainsFunc(res, func(re *regexp.Regexp) bool { return re.MatchString(s) })
}

// closesCell lists issues GitHub will close on merge. The keywords are
// GitHub's, so they aren't configurable.
func closesCell(p *pullRequest) string {
	var links []string
	seen := map[string]bool{}
	for _, m := range reCloses.FindAllStringSubmatch(cleanBody(p), -1) {
		repo, text := p.Repository.NameWithOwner, "#"+m[2]
		if m[1] != "" && !strings.EqualFold(m[1], repo) {
			repo, text = m[1], m[1]+"#"+m[2]
		}
		url := fmt.Sprintf("https://github.com/%s/issues/%s", repo, m[2])
		if !seen[url] {
			seen[url] = true
			links = append(links, fmt.Sprintf("[%s](%s)", text, url))
		}
	}
	if len(links) == 0 {
		return "—"
	}
	return strings.Join(links, ", ")
}

func (r *rules) testsCell(p *pullRequest) string {
	body := cleanBody(p)
	switch {
	case anyMatch(r.acc, body):
		return "✅"
	case anyMatch(r.unit, body):
		return "🧪"
	case anyMatch(r.claim, body):
		return "⚠️"
	default:
		return "❌"
	}
}

// reviewCell summarizes the latest Copilot review and whether any human other
// than the author has reviewed.
func (r *rules) reviewCell(p *pullRequest) string {
	d := r.cfg.Detect
	copilot := strings.ToLower(d.CopilotLogin)
	var summary string
	reviewed, human := false, false
	for _, rv := range p.Reviews.Nodes {
		l := rv.Author.Login
		switch {
		case strings.Contains(strings.ToLower(l), copilot):
			reviewed, summary = true, rv.Body // last one wins: reviews are oldest first
		case l == "", strings.HasSuffix(l, "[bot]"), strings.EqualFold(l, p.Author.Login),
			slices.ContainsFunc(d.IgnoreReviewers, func(x string) bool { return strings.EqualFold(x, l) }):
		default:
			human = true
		}
	}
	s := "❌"
	if reviewed {
		s = "✅"
		for _, v := range d.CopilotVerdicts {
			if strings.Contains(summary, v.Match) {
				s += " " + v.Symbol
				break
			}
		}
	}
	if human {
		s += " 👤"
	}
	return s
}

func fixUpCell(p *pullRequest) string {
	switch n := p.unresolvedThreads(); {
	case p.ReviewThreads.TotalCount == 0:
		return "—"
	case n == 0:
		return "✅"
	default:
		return fmt.Sprintf("❌ %d", n)
	}
}

// jogFor is the starting memory jogger for a new row: a shortened title plus
// tags for other repos and other authors. You're expected to edit it.
func (r *rules) jogFor(p *pullRequest, me string) string {
	d := r.cfg.Display
	t := strings.TrimSpace(strings.ReplaceAll(p.Title, "`", ""))
	if r.jogStrip != nil {
		t = strings.TrimSpace(r.jogStrip.ReplaceAllString(t, ""))
	}
	if w := strings.Fields(t); d.JogMaxWords > 0 && len(w) > d.JogMaxWords {
		t = strings.Join(w[:d.JogMaxWords], " ") + "…"
	}
	var tags []string
	if repo := p.Repository.NameWithOwner; !strings.EqualFold(repo, d.DefaultRepo) {
		tags = append(tags, repo[strings.Index(repo, "/")+1:])
	}
	if d.OtherAuthorTag != "" && !strings.EqualFold(p.Author.Login, me) {
		tags = append(tags, d.OtherAuthorTag)
	}
	if len(tags) > 0 {
		t += " (" + strings.Join(tags, ", ") + ")"
	}
	return strings.ReplaceAll(t, "|", `\|`)
}

func (r *rules) guessDependsOn(p *pullRequest) string {
	var links []string
	body := cleanBody(p)
	for _, re := range r.dependsOn {
		for _, m := range re.FindAllStringSubmatch(body, -1) {
			link := fmt.Sprintf("[#%s](https://github.com/%s/pull/%s)", m[1], p.Repository.NameWithOwner, m[1])
			if !slices.Contains(links, link) {
				links = append(links, link)
			}
		}
	}
	if len(links) == 0 {
		return "—"
	}
	return strings.Join(links, ", ")
}

func (r *rules) applyAuto(x *row, p *pullRequest) {
	x.Draft = p.IsDraft
	x.Closes = closesCell(p)
	x.Tests = r.testsCell(p)
	x.Review = r.reviewCell(p)
	x.FixUp = fixUpCell(p)
}

func (r *rules) dateCell(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "?"
	}
	return t.In(r.loc).Format("2006-01-02")
}

type syncer struct {
	gh    *github
	r     *rules
	me    string
	notes []string
}

func (s *syncer) note(format string, args ...any) {
	s.notes = append(s.notes, fmt.Sprintf(format, args...))
}

// mine returns PRs tied to me by any configured role that match qualifier,
// deduplicated, oldest first, and filtered to the configured repos.
func (s *syncer) mine(qualifier string) ([]*pullRequest, error) {
	var out []*pullRequest
	seen := map[string]bool{}
	for _, role := range s.r.cfg.Search.Roles {
		q := strings.Join(strings.Fields(fmt.Sprintf("is:pr %s %s:%s %s sort:created-asc",
			s.r.cfg.Search.ExtraQuery, role, s.me, qualifier)), " ")
		prs, err := s.gh.search(q)
		if err != nil {
			return nil, err
		}
		for _, p := range prs {
			if seen[p.URL] {
				continue
			}
			seen[p.URL] = true
			if !s.r.wantRepo(p.Repository.NameWithOwner, p.Repository.IsPrivate) {
				if p.Repository.IsPrivate && !s.r.cfg.Search.IncludePrivate && qualifier == "is:open" {
					s.note("skipped private %s (search.includePrivate is off; add the row by hand to track it)", p.URL)
				}
				continue
			}
			out = append(out, p)
		}
	}
	return out, nil
}

// sync reconciles both tables with GitHub. Rows already on the open page keep
// their position and hand-owned cells; rows whose PR merged move to the top
// of the merged table; closed-unmerged rows are dropped or recorded per
// config; new PRs go after the PR they stack on, or at the end.
func (s *syncer) sync(openRows, doneRows []*row, lastRun time.Time) ([]*row, []*row, error) {
	record := s.r.cfg.Display.ClosedUnmerged == "record"
	current, err := s.mine("is:open")
	if err != nil {
		return nil, nil, err
	}

	onOpen := map[string]bool{}
	inDone := map[string]bool{}
	for _, x := range doneRows {
		inDone[x.URL] = true
	}

	type ended struct {
		x *row
		p *pullRequest
	}
	var ends []ended
	var open []*row

	for _, x := range openRows {
		onOpen[x.URL] = true
		p, err := s.gh.lookup(x.URL)
		if err != nil {
			return nil, nil, err
		}
		switch {
		case p == nil:
			s.note("dropped %s: not found", x.URL)
		case p.State == "MERGED", p.State == "CLOSED" && record:
			ends = append(ends, ended{x, p})
		case p.State == "CLOSED":
			s.note("dropped %s: closed without merging", x.URL)
		default:
			s.r.applyAuto(x, p)
			open = append(open, x)
		}
	}

	// Catch PRs that were opened and finished between runs.
	if !lastRun.IsZero() {
		since := lastRun.UTC().Format("2006-01-02")
		quals := []string{"is:merged merged:>=" + since}
		if record {
			quals = append(quals, "is:closed is:unmerged closed:>="+since)
		}
		for _, q := range quals {
			recent, err := s.mine(q)
			if err != nil {
				return nil, nil, err
			}
			for _, p := range recent {
				if !onOpen[p.URL] && !inDone[p.URL] {
					ends = append(ends, ended{&row{URL: p.URL, Jog: s.r.jogFor(p, s.me)}, p})
				}
			}
		}
	}

	for _, p := range current {
		if onOpen[p.URL] {
			continue
		}
		x := &row{URL: p.URL, Jog: s.r.jogFor(p, s.me), Desc: "❔", DependsOn: s.r.guessDependsOn(p)}
		s.r.applyAuto(x, p)
		open = insertRow(open, x)
		s.note("added %s", p.URL)
	}

	slices.SortStableFunc(ends, func(a, b ended) int { return strings.Compare(b.p.endedAt(), a.p.endedAt()) })
	var newDone []*row
	page := s.r.mergedPageName()
	for _, e := range ends {
		if inDone[e.p.URL] {
			continue
		}
		inDone[e.p.URL] = true
		d := &row{URL: e.p.URL, Jog: e.x.Jog}
		if e.p.State == "MERGED" {
			d.Closes, d.Merged = closesCell(e.p), s.r.dateCell(e.p.MergedAt)
			s.note("merged %s → %s", e.p.URL, page)
		} else {
			d.Closes, d.Merged = "—", closedMark+" "+s.r.dateCell(e.p.ClosedAt)
			s.note("closed without merging %s → %s", e.p.URL, page)
		}
		newDone = append(newDone, d)
	}
	done := append(newDone, doneRows...)

	for _, x := range open {
		x.DependsOn = s.strikeMerged(x.DependsOn)
	}
	return open, done, nil
}

// insertRow places x after the PR it depends on (and that PR's other
// dependents) so merge order reads top to bottom; otherwise it appends.
func insertRow(rows []*row, x *row) []*row {
	m := reDepLink.FindStringSubmatch(x.DependsOn)
	if m == nil {
		return append(rows, x)
	}
	base := m[3]
	at := -1
	for i, y := range rows {
		if y.URL == base || strings.Contains(y.DependsOn, base) {
			at = i
		}
	}
	if at < 0 {
		return append(rows, x)
	}
	return slices.Insert(rows, at+1, x)
}

// strikeMerged strikes through dependency links whose PR has merged.
func (s *syncer) strikeMerged(cell string) string {
	return reDepLink.ReplaceAllStringFunc(cell, func(match string) string {
		m := reDepLink.FindStringSubmatch(match)
		if m[1] != "" { // already struck
			return match
		}
		p, err := s.gh.lookup(m[3])
		if err != nil {
			s.note("couldn't check dependency %s: %v", m[3], err)
			return match
		}
		if p != nil && p.State == "MERGED" {
			return "~~" + m[2] + "~~"
		}
		return match
	})
}
