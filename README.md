# zot-review

Structured repo-wide code review for [zot](https://www.zot.sh).

## Install

```bash
zot ext install https://github.com/patriceckhart/zot-review
```

Or from a local checkout:

```bash
mkdir -p "$HOME/Library/Application Support/zot/extensions/zot-review"
cp -R . "$HOME/Library/Application Support/zot/extensions/zot-review/"
```

Restart zot after installing or updating.

## Requirements

- Go 1.22+ on `$PATH` (the extension runs from source via `go run .`, no
  prebuilt binary is shipped). Install Go from <https://go.dev/dl>.

## Usage

Type:

```text
/review
```

This starts a structured review of the current project. The agent maps the repository into feature slices, reads relevant files, records concrete findings, and writes a Markdown report.

You can scope the review:

```text
/review auth and billing
/review app routes
/review packages/api
```

Other commands:

```text
/review-next
/review-report
```

| Command | Action |
|---------|--------|
| `/review [scope]` | Start a structured code review for the current project or scope |
| `/review-next` | Open the next open finding, ordered by severity, in a panel |
| `/review-report` | Open a colored, TUI-friendly findings report in a panel |

## Findings and reports

Review state is stored in the reviewed project directory:

```text
<project>/.codereview/
  findings/*.json
  reports/*.md
```

The panel report is optimized for zot's UI. Saved report files are Markdown and are written when the agent calls `render_report` with `write=true`, which `/review` asks it to do at the end of a review.

## Tools

The extension also registers LLM-callable tools:

| Tool | Action |
|------|--------|
| `map_features` | Detect coarse project slices: languages, frameworks, apps, packages, docs |
| `record_finding` | Persist a real, actionable finding |
| `list_findings` | List findings, optionally filtered by status or severity |
| `show_finding` | Show one finding by id |
| `triage_finding` | Mark a finding as `open`, `fixed`, `false-positive`, or `wontfix` |
| `next_finding` | Return the next open finding |
| `render_report` | Render findings as Markdown and optionally write the report |

## License

MIT
