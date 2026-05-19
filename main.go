// zot-review is a zot extension that helps with structured, repo-wide
// code review: map a project into feature slices, persist findings,
// triage them, and render a Markdown report. State lives under
// .codereview/ in the project root.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/patriceckhart/zot/pkg/zotext"
)

const stateDirName = ".codereview"

// ---------- domain types ----------

type Finding struct {
	ID         string    `json:"id"`
	Feature    string    `json:"feature"`
	Title      string    `json:"title"`
	Severity   string    `json:"severity"` // low | medium | high | critical
	Status     string    `json:"status"`   // open | fixed | false-positive | wontfix
	Path       string    `json:"path,omitempty"`
	Evidence   string    `json:"evidence,omitempty"`
	Suggestion string    `json:"suggestion,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Notes      []Note    `json:"notes,omitempty"`
}

type Note struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

type Feature struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"` // language / framework / role
	Roots       []string `json:"roots"`
	Description string   `json:"description,omitempty"`
}

// ---------- main ----------

func main() {
	ext := zotext.New("zot-review", "1.0.0")

	// --- slash commands ---

	ext.Command("review", "kick off a repo-wide structured code review", func(args string) zotext.Response {
		scope := strings.TrimSpace(args)
		if scope == "" {
			scope = "the entire repository"
		}
		return zotext.Prompt(reviewPrompt(scope))
	})

	ext.Command("review-report", "open the findings report in a panel", func(args string) zotext.Response {
		report, err := renderPanelReport(projectRoot(ext))
		if err != nil {
			return zotext.Errorf("report failed: %v", err)
		}
		if report == "" {
			return zotext.Display("no findings recorded yet. Run /review to start one.")
		}
		return zotext.OpenPanel("zot-review-report", "Code review findings", panelLines(report), "Esc/q close")
	})

	ext.Command("review-next", "open the next open finding in a panel", func(args string) zotext.Response {
		f, err := nextOpenFinding(projectRoot(ext))
		if err != nil {
			return zotext.Errorf("next failed: %v", err)
		}
		if f == nil {
			return zotext.Display("no open findings. Nice.")
		}
		return zotext.OpenPanel("zot-review-next", "Next code review finding", panelLines(formatFindingDetail(*f)), "Esc/q close")
	})

	// --- tools ---

	ext.Tool("map_features",
		"Detect coarse feature slices in the current repository (languages, frameworks, top-level packages, apps, routes). Returns a JSON array. Use this as the first step of a structured review so you know what to look at.",
		json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		func(args json.RawMessage) zotext.ToolResult {
			feats, err := mapFeatures(projectRoot(ext))
			if err != nil {
				return zotext.TextErrorResult(fmt.Sprintf("map_features: %v", err))
			}
			b, _ := json.MarshalIndent(feats, "", "  ")
			return zotext.TextResult(string(b))
		})

	ext.Tool("record_finding",
		"Persist a code-review finding under .codereview/findings/. Use this whenever you spot a real, actionable issue (bug, security, correctness, dead code, broken contract). Skip nitpicks and style.",
		json.RawMessage(`{
			"type":"object",
			"properties":{
				"feature":{"type":"string","description":"feature slice or area, e.g. \"auth\", \"go:cmd/server\""},
				"title":{"type":"string","description":"one-line summary of the issue"},
				"severity":{"type":"string","enum":["low","medium","high","critical"]},
				"path":{"type":"string","description":"path:line or path the finding refers to"},
				"evidence":{"type":"string","description":"short quote or paraphrase of the offending code/behavior"},
				"suggestion":{"type":"string","description":"concrete fix proposal"}
			},
			"required":["feature","title","severity"],
			"additionalProperties":false
		}`),
		func(args json.RawMessage) zotext.ToolResult {
			var in struct {
				Feature    string `json:"feature"`
				Title      string `json:"title"`
				Severity   string `json:"severity"`
				Path       string `json:"path"`
				Evidence   string `json:"evidence"`
				Suggestion string `json:"suggestion"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return zotext.TextErrorResult("invalid args: " + err.Error())
			}
			f := Finding{
				ID:         newID(),
				Feature:    in.Feature,
				Title:      in.Title,
				Severity:   in.Severity,
				Status:     "open",
				Path:       in.Path,
				Evidence:   in.Evidence,
				Suggestion: in.Suggestion,
				CreatedAt:  time.Now().UTC(),
				UpdatedAt:  time.Now().UTC(),
			}
			if err := saveFinding(projectRoot(ext), f); err != nil {
				return zotext.TextErrorResult(err.Error())
			}
			return zotext.TextResult(fmt.Sprintf("recorded finding %s (%s, %s): %s", f.ID, f.Severity, f.Feature, f.Title))
		})

	ext.Tool("list_findings",
		"List recorded findings, optionally filtered by status or severity. Returns JSON.",
		json.RawMessage(`{
			"type":"object",
			"properties":{
				"status":{"type":"string","enum":["open","fixed","false-positive","wontfix"]},
				"severity":{"type":"string","enum":["low","medium","high","critical"]}
			},
			"additionalProperties":false
		}`),
		func(args json.RawMessage) zotext.ToolResult {
			var in struct {
				Status   string `json:"status"`
				Severity string `json:"severity"`
			}
			_ = json.Unmarshal(args, &in)
			findings, err := loadFindings(projectRoot(ext))
			if err != nil {
				return zotext.TextErrorResult(err.Error())
			}
			out := make([]Finding, 0, len(findings))
			for _, f := range findings {
				if in.Status != "" && f.Status != in.Status {
					continue
				}
				if in.Severity != "" && f.Severity != in.Severity {
					continue
				}
				out = append(out, f)
			}
			b, _ := json.MarshalIndent(out, "", "  ")
			return zotext.TextResult(string(b))
		})

	ext.Tool("show_finding",
		"Return one finding by id with full evidence and note history.",
		json.RawMessage(`{
			"type":"object",
			"properties":{"id":{"type":"string"}},
			"required":["id"],
			"additionalProperties":false
		}`),
		func(args json.RawMessage) zotext.ToolResult {
			var in struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return zotext.TextErrorResult(err.Error())
			}
			f, err := loadFinding(projectRoot(ext), in.ID)
			if err != nil {
				return zotext.TextErrorResult(err.Error())
			}
			b, _ := json.MarshalIndent(f, "", "  ")
			return zotext.TextResult(string(b))
		})

	ext.Tool("triage_finding",
		"Update the status of a finding and optionally append a note.",
		json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string"},
				"status":{"type":"string","enum":["open","fixed","false-positive","wontfix"]},
				"note":{"type":"string"}
			},
			"required":["id","status"],
			"additionalProperties":false
		}`),
		func(args json.RawMessage) zotext.ToolResult {
			var in struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Note   string `json:"note"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return zotext.TextErrorResult(err.Error())
			}
			f, err := loadFinding(projectRoot(ext), in.ID)
			if err != nil {
				return zotext.TextErrorResult(err.Error())
			}
			f.Status = in.Status
			f.UpdatedAt = time.Now().UTC()
			if in.Note != "" {
				f.Notes = append(f.Notes, Note{At: time.Now().UTC(), Text: in.Note})
			}
			if err := saveFinding(projectRoot(ext), f); err != nil {
				return zotext.TextErrorResult(err.Error())
			}
			return zotext.TextResult(fmt.Sprintf("triaged %s -> %s", f.ID, f.Status))
		})

	ext.Tool("next_finding",
		"Return the next open finding to work on, severity-ordered (critical > high > medium > low), oldest first within a severity. Empty result means no open work.",
		json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		func(args json.RawMessage) zotext.ToolResult {
			f, err := nextOpenFinding(projectRoot(ext))
			if err != nil {
				return zotext.TextErrorResult(err.Error())
			}
			if f == nil {
				return zotext.TextResult("{}")
			}
			b, _ := json.MarshalIndent(f, "", "  ")
			return zotext.TextResult(string(b))
		})

	ext.Tool("render_report",
		"Render all findings as a Markdown report and return the text. Pass write=true to also write it to .codereview/reports/report-<ts>.md.",
		json.RawMessage(`{
			"type":"object",
			"properties":{"write":{"type":"boolean"}},
			"additionalProperties":false
		}`),
		func(args json.RawMessage) zotext.ToolResult {
			var in struct {
				Write bool `json:"write"`
			}
			_ = json.Unmarshal(args, &in)
			report, err := renderReport(projectRoot(ext))
			if err != nil {
				return zotext.TextErrorResult(err.Error())
			}
			if in.Write {
				path, err := writeReport(projectRoot(ext), report)
				if err != nil {
					return zotext.TextErrorResult(err.Error())
				}
				return zotext.TextResult(report + "\n\n_wrote " + path + "_")
			}
			return zotext.TextResult(report)
		})

	if err := ext.Run(); err != nil {
		ext.Logf("fatal: %v", err)
		os.Exit(1)
	}
}

// ---------- prompts ----------

func reviewPrompt(scope string) string {
	return strings.TrimSpace(`
Perform a structured code review of ` + scope + ` using the zot-review extension tools.

Process:
1. Call ` + "`map_features`" + ` once to enumerate the repo's coarse slices (languages, apps, packages, routes).
2. Pick the slices that look most likely to contain real defects (security, correctness, broken contracts, dead code, leaked secrets, footguns). Skip generated code, vendored deps, and lockfiles.
3. For each chosen slice, read the relevant files and reason about them. Do not guess; open files before claiming anything about them.
4. Whenever you spot a concrete, actionable issue, call ` + "`record_finding`" + ` with a precise title, the file path (with line if known), a short evidence snippet, and a suggested fix. Severity rubric:
   - critical: data loss, security hole, broken auth, remote crash
   - high: incorrect behaviour users will hit, broken contract, silent data corruption
   - medium: latent bug, missing error handling, race, leak
   - low: hygiene-but-still-real issue (dead code, misleading naming, wrong log level)
5. Do NOT record style nits, formatting opinions, or speculative "could be cleaner" notes. If you cannot point at a specific file and a concrete consequence, drop it.
6. When you are done, call ` + "`render_report`" + ` with write=true and summarise the top findings in one short paragraph.

Be terse. Use the tools; do not paste long file contents into chat.
`)
}

// ---------- feature mapping ----------

// mapFeatures does a fast, conservative pass over the repo root to
// detect coarse slices. It intentionally does not try to be exhaustive
// — the LLM will read files itself; this just gives it a starting map.
func mapFeatures(root string) ([]Feature, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var feats []Feature
	add := func(f Feature) { feats = append(feats, f) }

	// quick existence checks at root
	if exists(filepath.Join(root, "go.mod")) {
		add(Feature{Name: "go", Kind: "language", Roots: []string{"."}, Description: "Go module"})
		// scan cmd/*
		for _, d := range subdirs(filepath.Join(root, "cmd")) {
			add(Feature{Name: "go:cmd/" + d, Kind: "binary", Roots: []string{"cmd/" + d}})
		}
	}
	if exists(filepath.Join(root, "package.json")) {
		add(Feature{Name: "node", Kind: "language", Roots: []string{"."}, Description: "Node/JS project"})
		// Next.js
		if exists(filepath.Join(root, "next.config.js")) || exists(filepath.Join(root, "next.config.ts")) || exists(filepath.Join(root, "next.config.mjs")) {
			add(Feature{Name: "nextjs", Kind: "framework", Roots: []string{"app", "pages"}, Description: "Next.js app"})
		}
	}
	if exists(filepath.Join(root, "pyproject.toml")) || exists(filepath.Join(root, "setup.py")) {
		add(Feature{Name: "python", Kind: "language", Roots: []string{"."}})
	}
	if exists(filepath.Join(root, "Cargo.toml")) {
		add(Feature{Name: "rust", Kind: "language", Roots: []string{"."}})
	}
	if exists(filepath.Join(root, "Gemfile")) {
		add(Feature{Name: "ruby", Kind: "language", Roots: []string{"."}})
	}
	if exists(filepath.Join(root, "composer.json")) {
		add(Feature{Name: "php", Kind: "language", Roots: []string{"."}})
	}
	if exists(filepath.Join(root, "mix.exs")) {
		add(Feature{Name: "elixir", Kind: "language", Roots: []string{"."}})
	}
	if exists(filepath.Join(root, "pom.xml")) || exists(filepath.Join(root, "build.gradle")) || exists(filepath.Join(root, "build.gradle.kts")) {
		add(Feature{Name: "jvm", Kind: "language", Roots: []string{"."}})
	}
	if exists(filepath.Join(root, "Package.swift")) {
		add(Feature{Name: "swift", Kind: "language", Roots: []string{"."}})
	}

	// monorepo apps / packages
	for _, dir := range []string{"apps", "packages", "services", "crates", "extensions", "plugins"} {
		for _, sub := range subdirs(filepath.Join(root, dir)) {
			add(Feature{Name: dir + "/" + sub, Kind: "workspace", Roots: []string{dir + "/" + sub}})
		}
	}

	// docs
	if exists(filepath.Join(root, "docs")) {
		add(Feature{Name: "docs", Kind: "docs", Roots: []string{"docs"}})
	}
	if exists(filepath.Join(root, "README.md")) {
		add(Feature{Name: "readme", Kind: "docs", Roots: []string{"README.md"}})
	}

	if len(feats) == 0 {
		add(Feature{Name: "repo", Kind: "fallback", Roots: []string{"."}, Description: "no recognised project markers; review by directory"})
	}
	return feats, nil
}

func subdirs(path string) []string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ---------- state on disk ----------

func projectRoot(ext *zotext.Extension) string {
	if cwd := ext.Host().CWD; cwd != "" {
		return cwd
	}
	wd, err := os.Getwd()
	if err == nil {
		return wd
	}
	return "."
}

func ensureStateDirs(root string) (string, error) {
	if root == "" {
		root = "."
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, stateDirName)
	if err := os.MkdirAll(filepath.Join(dir, "findings"), 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(dir, "reports"), 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func saveFinding(root string, f Finding) error {
	dir, err := ensureStateDirs(root)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "findings", f.ID+".json")
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func loadFinding(root string, id string) (Finding, error) {
	dir, err := ensureStateDirs(root)
	if err != nil {
		return Finding{}, err
	}
	path := filepath.Join(dir, "findings", id+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return Finding{}, fmt.Errorf("finding %q not found", id)
	}
	var f Finding
	if err := json.Unmarshal(b, &f); err != nil {
		return Finding{}, err
	}
	return f, nil
}

func loadFindings(root string) ([]Finding, error) {
	dir, err := ensureStateDirs(root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, "findings"))
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "findings", e.Name()))
		if err != nil {
			continue
		}
		var f Finding
		if json.Unmarshal(b, &f) == nil {
			out = append(out, f)
		}
	}
	return out, nil
}

func nextOpenFinding(root string) (*Finding, error) {
	all, err := loadFindings(root)
	if err != nil {
		return nil, err
	}
	weight := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	open := make([]Finding, 0, len(all))
	for _, f := range all {
		if f.Status == "open" {
			open = append(open, f)
		}
	}
	if len(open) == 0 {
		return nil, nil
	}
	sort.SliceStable(open, func(i, j int) bool {
		wi, wj := weight[open[i].Severity], weight[open[j].Severity]
		if wi != wj {
			return wi < wj
		}
		return open[i].CreatedAt.Before(open[j].CreatedAt)
	})
	return &open[0], nil
}

// ---------- reporting ----------

func renderReport(root string) (string, error) {
	findings, err := loadFindings(root)
	if err != nil {
		return "", err
	}
	if len(findings) == 0 {
		return "", nil
	}
	weight := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.SliceStable(findings, func(i, j int) bool {
		wi, wj := weight[findings[i].Severity], weight[findings[j].Severity]
		if wi != wj {
			return wi < wj
		}
		if findings[i].Feature != findings[j].Feature {
			return findings[i].Feature < findings[j].Feature
		}
		return findings[i].CreatedAt.Before(findings[j].CreatedAt)
	})

	var counts = map[string]int{}
	var statusCounts = map[string]int{}
	for _, f := range findings {
		counts[f.Severity]++
		statusCounts[f.Status]++
	}

	var b strings.Builder
	b.WriteString("# Code review findings\n\n")
	fmt.Fprintf(&b, "Total: %d  ·  critical: %d  ·  high: %d  ·  medium: %d  ·  low: %d\n",
		len(findings), counts["critical"], counts["high"], counts["medium"], counts["low"])
	fmt.Fprintf(&b, "Open: %d  ·  fixed: %d  ·  false-positive: %d  ·  wontfix: %d\n\n",
		statusCounts["open"], statusCounts["fixed"], statusCounts["false-positive"], statusCounts["wontfix"])

	currentSev := ""
	for _, f := range findings {
		if f.Severity != currentSev {
			currentSev = f.Severity
			fmt.Fprintf(&b, "## %s\n\n", strings.ToUpper(currentSev))
		}
		fmt.Fprintf(&b, "### %s — %s  (`%s`, %s)\n\n", f.Title, f.Feature, f.ID, f.Status)
		if f.Path != "" {
			fmt.Fprintf(&b, "- path: `%s`\n", f.Path)
		}
		if f.Evidence != "" {
			fmt.Fprintf(&b, "- evidence: %s\n", f.Evidence)
		}
		if f.Suggestion != "" {
			fmt.Fprintf(&b, "- suggestion: %s\n", f.Suggestion)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func renderPanelReport(root string) (string, error) {
	findings, err := loadFindings(root)
	if err != nil {
		return "", err
	}
	if len(findings) == 0 {
		return "", nil
	}
	weight := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.SliceStable(findings, func(i, j int) bool {
		wi, wj := weight[findings[i].Severity], weight[findings[j].Severity]
		if wi != wj {
			return wi < wj
		}
		if findings[i].Status != findings[j].Status {
			return findings[i].Status < findings[j].Status
		}
		return findings[i].CreatedAt.Before(findings[j].CreatedAt)
	})

	counts := map[string]int{}
	statusCounts := map[string]int{}
	for _, f := range findings {
		counts[f.Severity]++
		statusCounts[f.Status]++
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Summary: %d total  |  %d critical  %d high  %d medium  %d low\n",
		len(findings), counts["critical"], counts["high"], counts["medium"], counts["low"])
	fmt.Fprintf(&b, "Status:  %d open   |  %d fixed  %d false-positive  %d wontfix\n",
		statusCounts["open"], statusCounts["fixed"], statusCounts["false-positive"], statusCounts["wontfix"])
	b.WriteString(strings.Repeat("─", 96))
	b.WriteString("\n")

	current := ""
	for i, f := range findings {
		if f.Severity != current {
			if i > 0 {
				b.WriteString("\n")
			}
			current = f.Severity
			fmt.Fprintf(&b, "%s\n", colorSeverity(current, strings.ToUpper(current)))
			b.WriteString(colorDim(strings.Repeat("─", len(current))))
			b.WriteString("\n")
		}

		fmt.Fprintf(&b, "\n%s %s\n", colorDim("["+f.ID+"]"), colorTitle(f.Title))
		meta := []string{}
		if f.Feature != "" {
			meta = append(meta, f.Feature)
		}
		if f.Status != "" {
			meta = append(meta, f.Status)
		}
		if f.Path != "" {
			meta = append(meta, f.Path)
		}
		if len(meta) > 0 {
			fmt.Fprintf(&b, "  %s\n", colorMeta(strings.Join(meta, "  ·  ")))
		}
		if f.Evidence != "" {
			b.WriteString("  " + colorLabel("evidence:") + "\n")
			b.WriteString(indent(wrapText(f.Evidence, 110), "    "))
			b.WriteString("\n")
		}
		if f.Suggestion != "" {
			b.WriteString("  " + colorLabel("fix:") + "\n")
			b.WriteString(indent(wrapText(f.Suggestion, 110), "    "))
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

func writeReport(root string, content string) (string, error) {
	dir, err := ensureStateDirs(root)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("report-%s.md", time.Now().UTC().Format("20060102-150405"))
	path := filepath.Join(dir, "reports", name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(filepath.Dir(dir), path)
	if err != nil {
		return path, nil
	}
	return rel, nil
}

func formatFindingShort(f Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** — %s  (`%s`, %s, %s)\n", f.Title, f.Feature, f.ID, f.Severity, f.Status)
	if f.Path != "" {
		fmt.Fprintf(&b, "- path: `%s`\n", f.Path)
	}
	if f.Suggestion != "" {
		fmt.Fprintf(&b, "- suggestion: %s\n", f.Suggestion)
	}
	return b.String()
}

func formatFindingDetail(f Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", f.Title)
	fmt.Fprintf(&b, "id:       %s\n", f.ID)
	fmt.Fprintf(&b, "feature:  %s\n", f.Feature)
	fmt.Fprintf(&b, "severity: %s\n", f.Severity)
	fmt.Fprintf(&b, "status:   %s\n", f.Status)
	if f.Path != "" {
		fmt.Fprintf(&b, "path:     %s\n", f.Path)
	}
	if f.Evidence != "" {
		fmt.Fprintf(&b, "\nevidence:\n%s\n", wrapText(f.Evidence, 100))
	}
	if f.Suggestion != "" {
		fmt.Fprintf(&b, "\nsuggestion:\n%s\n", wrapText(f.Suggestion, 100))
	}
	if len(f.Notes) > 0 {
		b.WriteString("\nnotes:\n")
		for _, n := range f.Notes {
			fmt.Fprintf(&b, "- %s: %s\n", n.At.Format(time.RFC3339), n.Text)
		}
	}
	return b.String()
}

func panelLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func wrapText(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return s
	}
	var b strings.Builder
	lineLen := 0
	for _, w := range words {
		if lineLen > 0 && lineLen+1+len(w) > width {
			b.WriteByte('\n')
			lineLen = 0
		}
		if lineLen > 0 {
			b.WriteByte(' ')
			lineLen++
		}
		b.WriteString(w)
		lineLen += len(w)
	}
	return b.String()
}

func indent(s string, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

func ansi256(code int, s string) string {
	return fmt.Sprintf("\x1b[38;5;%dm%s\x1b[0m", code, s)
}

func colorTitle(s string) string { return ansi256(153, s) }
func colorMeta(s string) string  { return ansi256(110, s) }
func colorLabel(s string) string { return ansi256(180, s) }
func colorDim(s string) string   { return ansi256(244, s) }

func colorSeverity(severity string, s string) string {
	switch severity {
	case "critical":
		return ansi256(196, s)
	case "high":
		return ansi256(208, s)
	case "medium":
		return ansi256(220, s)
	case "low":
		return ansi256(109, s)
	default:
		return ansi256(153, s)
	}
}

// ---------- id ----------

func newID() string {
	// short, sortable-ish id from time + nano randomness already
	// provided by monotonic clock at nanosecond resolution.
	now := time.Now().UTC()
	return fmt.Sprintf("f_%s_%04d", now.Format("20060102150405"), now.Nanosecond()%10000)
}
