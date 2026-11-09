package main

// The stress test's report: report.json (one record per check, for further
// processing) and report.html (the same, to read), written from the same data.

import (
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// stressResult is one check. Result is "pass", "fail" or "info" (an
// observation, e.g. which conflicts Zotero's window would have shown).
type stressResult struct {
	Library   string    `json:"library"`
	Scenario  string    `json:"scenario"`
	Operation string    `json:"operation"`
	Check     string    `json:"check"`
	Result    string    `json:"result"`
	Detail    string    `json:"detail,omitempty"`
	Time      time.Time `json:"time"`
}

type stressLibrary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SafetyBackup string `json:"safety_backup"`
}

type stressSummary struct {
	Checks int `json:"checks"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
	Info   int `json:"observations"`
}

type stressScenarioInfo struct {
	Name     string `json:"name"`
	Sequence string `json:"sequence"`
	Expected string `json:"expected"`
}

type stressReport struct {
	mu         sync.Mutex
	Run        string               `json:"run"`
	Tool       string               `json:"tool_version"`
	System     string               `json:"system"`
	Started    time.Time            `json:"started"`
	Finished   time.Time            `json:"finished"`
	Backups    string               `json:"backups"`
	Libraries  []stressLibrary      `json:"libraries"`
	Scenarios  []string             `json:"scenarios_selected"`
	Operations []string             `json:"operations_selected"`
	Summary    stressSummary        `json:"summary"`
	Described  []stressScenarioInfo `json:"scenarios"`
	Results    []stressResult       `json:"results"`
}

func (r *stressReport) add(x stressResult) {
	r.mu.Lock()
	r.Results = append(r.Results, x)
	r.mu.Unlock()
}

func (r *stressReport) summary() stressSummary {
	var s stressSummary
	for _, x := range r.Results {
		switch x.Result {
		case "pass":
			s.Passed++
		case "fail":
			s.Failed++
		default:
			s.Info++
		}
	}
	s.Checks = s.Passed + s.Failed
	return s
}

// write puts report.json and report.html into dir.
func (r *stressReport) write(dir string) error {
	r.Summary = r.summary()
	r.Described = nil
	for _, sc := range stressScenarios {
		r.Described = append(r.Described, stressScenarioInfo{sc.name, sc.desc, sc.expect})
	}
	if r.Results == nil {
		r.Results = []stressResult{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "report.html"))
	if err != nil {
		return err
	}
	defer f.Close()
	return reportHTML.Execute(f, r)
}

var reportHTML = template.Must(template.New("report").Funcs(template.FuncMap{
	"when": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.Format("2006-01-02 15:04:05 UTC")
	},
	"failed": func(rs []stressResult) []stressResult {
		var out []stressResult
		for _, x := range rs {
			if x.Result == "fail" {
				out = append(out, x)
			}
		}
		return out
	},
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>zotero-backup stress test {{.Run}}</title>
<style>
:root{--ink:#1f2328;--quiet:#59636e;--line:#d1d9e0;--bg:#fff;--pass:#1a7f37;--fail:#cf222e;--info:#0969da;--tint:#f6f8fa}
@media (prefers-color-scheme:dark){:root{--ink:#e6edf3;--quiet:#9198a1;--line:#3d444d;--bg:#0d1117;--pass:#3fb950;--fail:#f85149;--info:#4493f8;--tint:#151b23}}
body{margin:0;padding:24px 16px;background:var(--bg);color:var(--ink);font:14px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:1100px;margin:0 auto}h1{font-size:22px;margin:0 0 4px}h2{font-size:17px;margin:28px 0 8px}
.meta{color:var(--quiet)}.tiles{display:flex;gap:12px;flex-wrap:wrap;margin:16px 0}
.tile{border:1px solid var(--line);border-radius:8px;padding:10px 16px;min-width:110px}.tile b{display:block;font-size:22px}
table{border-collapse:collapse;width:100%;font-size:13px}th,td{text-align:left;padding:6px 8px;border-bottom:1px solid var(--line);vertical-align:top}
th{background:var(--tint)}td.r{font-weight:600;white-space:nowrap}.pass{color:var(--pass)}.fail{color:var(--fail)}.info{color:var(--info)}
code{font-size:12px}.wrap{overflow-x:auto}
</style></head><body><main>
<h1>zotero-backup stress test</h1>
<div class="meta">run {{.Run}} · zotero-backup {{.Tool}} · {{.System}} · {{when .Started}} to {{when .Finished}}</div>
<div class="tiles">
<div class="tile"><b>{{.Summary.Checks}}</b>checks</div>
<div class="tile pass"><b>{{.Summary.Passed}}</b>passed</div>
<div class="tile fail"><b>{{.Summary.Failed}}</b>failed</div>
<div class="tile info"><b>{{.Summary.Info}}</b>observations</div>
</div>
<h2>Libraries</h2>
<div class="wrap"><table><tr><th>Library</th><th>Name</th><th>Safety backup</th></tr>
{{range .Libraries}}<tr><td>{{.ID}}</td><td>{{.Name}}</td><td><code>{{.SafetyBackup}}</code></td></tr>{{end}}
</table></div>
<p class="meta">Backups of this run: <code>{{.Backups}}</code></p>
{{with failed .Results}}<h2 class="fail">Failed checks</h2>
<div class="wrap"><table><tr><th>Library</th><th>Scenario</th><th>Operation</th><th>Check</th><th>Detail</th></tr>
{{range .}}<tr><td>{{.Library}}</td><td>{{.Scenario}}</td><td>{{.Operation}}</td><td>{{.Check}}</td><td>{{.Detail}}</td></tr>{{end}}
</table></div>{{end}}
<h2>Scenarios</h2>
<div class="wrap"><table><tr><th>Scenario</th><th>Sequence</th><th>Expected</th></tr>
{{range .Described}}<tr><td><code>{{.Name}}</code></td><td>{{.Sequence}}</td><td>{{.Expected}}</td></tr>{{end}}
</table></div>
<h2>All checks</h2>
<div class="wrap"><table><tr><th>Library</th><th>Scenario</th><th>Operation</th><th>Check</th><th>Result</th><th>Detail</th></tr>
{{range .Results}}<tr><td>{{.Library}}</td><td>{{.Scenario}}</td><td>{{.Operation}}</td><td>{{.Check}}</td><td class="r {{.Result}}">{{if eq .Result "pass"}}✔ pass{{else if eq .Result "fail"}}✘ fail{{else}}ℹ info{{end}}</td><td>{{.Detail}}</td></tr>{{end}}
</table></div>
</main></body></html>
`))
