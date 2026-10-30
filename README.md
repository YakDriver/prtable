# prtable

`prtable` keeps two tables on a GitHub wiki up to date:

- **In process**, on one page: open PRs you wrote or are assigned to, with draft status, linked issues, test output, Copilot and human review, and unresolved review threads.
- **Merged**, at the top of another page: PRs that merged, newest first.

You run it by hand. Each run reads the current state from GitHub, refreshes the generated columns, moves merged PRs to the merged table, adds new PRs, and pushes the wiki. If nothing changed, it pushes nothing.

## Requirements

- Go 1.26+
- [`gh`](https://cli.github.com), logged in (`gh auth login`). `prtable` uses its credentials for GitHub's API and `git` for the wiki push.
- A GitHub wiki you can push to. Create at least one page in the web UI first so the wiki's git repo exists.

## Setup

```sh
go build -o prtable .   # in a clone of this repo; put the binary on your PATH
prtable -init        # writes ~/.config/prtable/config.hcl
$EDITOR ~/.config/prtable/config.hcl   # set wiki.url at minimum
prtable -dry-run     # shows the wiki diff without pushing
prtable              # pushes
```

`-init` writes every setting with its default and a comment explaining it. Delete what you don't need to change. See [`starter.hcl`](starter.hcl).

## Config file location

The first of these that applies:

1. `-config path/to/file.hcl`
2. `$PRTABLE_CONFIG`
3. `./prtable.hcl`
4. `$XDG_CONFIG_HOME/prtable/config.hcl`, or `~/.config/prtable/config.hcl` if `XDG_CONFIG_HOME` isn't set

Unknown or misspelled settings are errors, so a typo can't be silently ignored.

## What you edit vs. what's generated

| Column | Source |
|---|---|
| Memory jogger (text before the PR link) | Yours. New rows start from the PR title (see `display.jog_*`). |
| Desc | Yours. New rows start as ❔. |
| Depends On | Yours. New rows are guessed from `detect.depends_on`. Links to merged PRs are struck through automatically. |
| Draft 🚧, Closes, Tests, Review, Fix Up | Regenerated every run. |
| Row order | Yours. New rows go after the PR they depend on, or at the end. |

`prtable` only rewrites text between its `<!-- prtable:... -->` markers, which are invisible on the rendered page. The rest of both pages is untouched. On the first run it adds the markers: under `wiki.open_heading` if the open page has that heading (adopting a hand-built table there), otherwise at the top of the page.

## Example: a Terraform provider maintainer

```hcl
wiki {
  url         = "https://github.com/me/notes.wiki.git"
  merged_page = "Done.md"
}

search {
  include_repos = ["hashicorp/*"]
}

display {
  default_repo = "hashicorp/terraform-provider-aws"
  jog_strip    = "(?i)^(?:[rd]/|new (?:resource|data source):\\s*)"
}

detect {
  acceptance_output = ["--- PASS: TestAcc", "(?s)(?:TF_ACC=1|make testacc).*\\bPASS\\b"]
}
```

## Privacy

Wikis on public repos are public. By default `prtable` skips PRs in private repos (`search.include_private = false`) so their titles aren't published.

## Development

`make ci` runs the same checks as CI: build, gofmt, `go fix`, `go mod tidy`, vet, and tests. `make help` lists every target, including `make fmt`, `make fix`, and `make tidy` to apply fixes.
