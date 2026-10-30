# prtable configuration. Every setting below shows its default; delete any
# you don't need to change. A list or repeated block you set replaces the
# default instead of adding to it.
#
# Regexes use Go RE2 syntax inside HCL strings, so backslashes are doubled
# ("\\b"), and a literal "${" must be written "$${".

wiki {
  # Git URL of the wiki to update. Required.
  url = "https://github.com/OWNER/REPO.wiki.git"

  # Wiki file holding the "in process" table, and the file holding merged
  # PRs. Either is created if it doesn't exist.
  open_page   = "Home.md"
  merged_page = "Merged-PRs.md"

  # On the first run, the open table goes under this heading (adopting a
  # hand-built table already there). If the page has no such heading, the
  # heading and table go at the top of the page.
  open_heading = "## PRs in process"

  # The merged table goes at the top of merged_page under merged_heading.
  # Whatever the page held before goes under other_heading (set it to "" to
  # add no heading).
  merged_heading = "## Merged PRs"
  other_heading  = "## Other completed"

  commit_message = "prtable: sync PR tables"
}

search {
  # GitHub search qualifiers that tie a PR to you: author, assignee,
  # review-requested, reviewed-by, commenter, mentions, involves.
  roles = ["author", "assignee"]

  # owner/name globs, e.g. "hashicorp/*". An empty include list means every
  # repo; exclude wins over include.
  include_repos = []
  exclude_repos = []

  # Leave this off if your wiki is public: titles of private PRs would be
  # published. A row you add by hand is still refreshed either way.
  include_private = false

  # Added to every search.
  extra_query = "archived:false"
}

display {
  # PRs in this repo get no repo tag in their memory jogger; others get
  # "(repo-name)". If empty, every row is tagged.
  default_repo = ""

  # IANA zone for dates, e.g. "America/New_York". Empty means the system zone.
  timezone = ""

  # New memory joggers are the PR title, with jog_strip (a regex) removed
  # and cut to jog_max_words words (0 = no limit). You edit them after.
  jog_max_words = 6
  jog_strip     = ""

  # Tag for PRs someone else wrote, e.g. ones you're assigned to. "" turns
  # it off.
  other_author_tag = "community"

  # What happens to a PR closed without merging: "drop" removes it;
  # "record" lists it on the merged page marked ✖️.
  closed_unmerged = "drop"

  # Extra text appended to the Fix Up part of the legend.
  legend_note = ""
}

detect {
  # Reviewers whose login contains this are treated as Copilot.
  copilot_login = "copilot"

  # Text in Copilot's latest review summary that sets its verdict symbol.
  # The first match wins.
  copilot_verdict {
    match  = "Approval recommended"
    symbol = "🟢"
    label  = "approve"
  }
  copilot_verdict {
    match  = "Changes recommended"
    symbol = "🟡"
    label  = "changes"
  }
  copilot_verdict {
    match  = "Needs a closer look"
    symbol = "🔵"
    label  = "closer look"
  }

  # Not counted as human reviews. Logins ending in [bot] and the PR's
  # author are always ignored.
  ignore_reviewers = ["github-actions"]

  # Regexes run against the PR description (HTML comments removed), in
  # order: acceptance output ✅, unit output 🧪, a claim of passing tests
  # with no output ⚠️, otherwise ❌.
  acceptance_output = []
  unit_output       = ["(?m)--- PASS: Test", "(?m)^ok\\s+\\S"]
  test_claim        = ["(?i)\\btests?\\s+pass"]

  # Regexes that guess Depends On for new rows. Each needs one capture
  # group holding the PR number.
  depends_on = ["(?i)\\b(?:stack(?:ed|s)?\\s+on|based\\s+on|depends\\s+on)\\s+#(\\d+)\\b"]
}
