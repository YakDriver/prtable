// Command prtable keeps PR tracking tables on a GitHub wiki current.
//
// It finds open PRs tied to you (author, assignee, ... per config), refreshes
// an "in process" table on one wiki page, and moves merged PRs to the top of
// a table on another. Hand-edited cells (memory jogger, Desc, Depends On) and
// row order are preserved. Optionally, it also keeps an issues table on the
// open page. Everything about your setup lives in an HCL config
// file; run prtable -init to write a commented starter.
//
// Usage:
//
//	prtable -init       # write a starter config, then edit it
//	prtable -dry-run    # show the wiki diff, change nothing
//	prtable             # commit and push the wiki
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	configFlag := flag.String("config", "", "config file (default: $PRTABLE_CONFIG, ./prtable.hcl, or ~/.config/prtable/config.hcl)")
	initFlag := flag.Bool("init", false, "write a commented starter config to the config path and exit")
	dryRun := flag.Bool("dry-run", false, "print the wiki diff without committing or pushing")
	wikiDir := flag.String("wiki-dir", "", "use (and pull) an existing wiki clone instead of a temporary one")
	flag.Parse()

	if err := start(*configFlag, *initFlag, *wikiDir, *dryRun); err != nil {
		fmt.Fprintln(os.Stderr, "prtable:", err)
		os.Exit(1)
	}
}

func start(configFlag string, initOnly bool, wikiDir string, dryRun bool) error {
	p, err := configPath(configFlag)
	if err != nil {
		return err
	}
	if initOnly {
		if err := writeStarter(p); err != nil {
			return err
		}
		fmt.Printf("wrote %s; set wiki.url, then run prtable -dry-run\n", p)
		return nil
	}
	cfg, err := loadConfig(p)
	if err != nil {
		return err
	}
	r, err := cfg.compile()
	if err != nil {
		return fmt.Errorf("%s:\n%w", p, err)
	}
	return run(r, wikiDir, dryRun)
}

func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// readPage returns a wiki page's contents, or "" if it doesn't exist yet.
func readPage(p string) (string, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

func run(r *rules, dir string, dryRun bool) error {
	w := r.cfg.Wiki
	if dir == "" {
		tmp, err := os.MkdirTemp("", "prtable-wiki-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		if out, err := exec.Command("git", "clone", "-q", w.URL, tmp).CombinedOutput(); err != nil {
			return fmt.Errorf("cloning %s: %w: %s", w.URL, err, strings.TrimSpace(string(out)))
		}
		dir = tmp
	} else if _, err := git(dir, "pull", "-q", "--ff-only"); err != nil {
		return err
	}

	openPath, donePath := filepath.Join(dir, w.OpenPage), filepath.Join(dir, w.MergedPage)
	openRaw, err := readPage(openPath)
	if err != nil {
		return err
	}
	doneRaw, err := readPage(donePath)
	if err != nil {
		return err
	}

	open := ensureOpenBlock(openRaw, w.OpenHeading)
	done := ensureDoneBlock(doneRaw, w.MergedHeading, w.OtherHeading)
	withIssues := r.cfg.Issues.enabled()
	if withIssues {
		open = ensureIssuesBlock(open, r.cfg.Issues.Heading)
	}

	openBlock, err := extractBlock(open, openStart, openEnd)
	if err != nil {
		return fmt.Errorf("%s: %w", w.OpenPage, err)
	}
	doneBlock, err := extractBlock(done, doneStart, doneEnd)
	if err != nil {
		return fmt.Errorf("%s: %w", w.MergedPage, err)
	}
	openRows, err := parseOpenRows(openBlock)
	if err != nil {
		return fmt.Errorf("%s: %w", w.OpenPage, err)
	}
	doneRows, err := parseDoneRows(doneBlock)
	if err != nil {
		return fmt.Errorf("%s: %w", w.MergedPage, err)
	}
	lastRun := parseUpdated(openBlock)
	var issueRows []*issueRow
	if withIssues {
		issuesBlock, err := extractBlock(open, issuesStart, issuesEnd)
		if err != nil {
			return fmt.Errorf("%s: %w", w.OpenPage, err)
		}
		if issueRows, err = parseIssueRows(issuesBlock); err != nil {
			return fmt.Errorf("%s: %w", w.OpenPage, err)
		}
	}

	gh := newGitHub()
	me, err := gh.login()
	if err != nil {
		return err
	}
	s := &syncer{gh: gh, r: r, me: me}
	openRows, doneRows, err = s.sync(openRows, doneRows, lastRun)
	if err != nil {
		return err
	}
	if withIssues {
		if issueRows, err = s.syncIssues(issueRows); err != nil {
			return err
		}
	}
	for _, n := range s.notes {
		fmt.Println("•", n)
	}

	// Render with the previous timestamp first so a run that changes nothing
	// else doesn't produce a timestamp-only commit.
	stamp := lastRun
	if stamp.IsZero() {
		stamp = time.Now()
	}
	t := tables{open: openRows, done: doneRows, issues: issueRows, withIssues: withIssues}
	newOpen, newDone, err := render(r, open, done, t, stamp)
	if err != nil {
		return err
	}
	if newOpen == openRaw && newDone == doneRaw {
		fmt.Println("wiki already up to date")
		return nil
	}
	if newOpen, newDone, err = render(r, open, done, t, time.Now()); err != nil {
		return err
	}

	if err := os.WriteFile(openPath, []byte(newOpen), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(donePath, []byte(newDone), 0o644); err != nil {
		return err
	}

	if dryRun {
		// -N so a newly created page shows up in the diff.
		if _, err := git(dir, "add", "-N", w.OpenPage, w.MergedPage); err != nil {
			return err
		}
		diff, err := git(dir, "--no-pager", "diff")
		if err != nil {
			return err
		}
		fmt.Print(diff)
		fmt.Println("(dry run: nothing committed or pushed)")
		return nil
	}

	if _, err := git(dir, "add", w.OpenPage, w.MergedPage); err != nil {
		return err
	}
	if _, err := git(dir, "commit", "-q", "-m", w.CommitMessage); err != nil {
		return err
	}
	if _, err := git(dir, "push", "-q", "origin", "HEAD"); err != nil {
		return err
	}
	fmt.Printf("pushed: %d in process, %d merged", len(openRows), len(doneRows))
	if withIssues {
		fmt.Printf(", %d issues", len(issueRows))
	}
	fmt.Println()
	return nil
}

// tables is the synced content of every generated block.
type tables struct {
	open, done []*row
	issues     []*issueRow
	withIssues bool
}

func render(r *rules, open, done string, t tables, stamp time.Time) (string, string, error) {
	o, err := replaceBlock(open, openStart, openEnd, r.renderOpen(t.open, stamp))
	if err != nil {
		return "", "", err
	}
	if t.withIssues {
		if o, err = replaceBlock(o, issuesStart, issuesEnd, r.renderIssues(t.issues)); err != nil {
			return "", "", err
		}
	}
	d, err := replaceBlock(done, doneStart, doneEnd, r.renderDone(t.done))
	if err != nil {
		return "", "", err
	}
	return o, d, nil
}
