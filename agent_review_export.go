package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// report.sarif and report.html, made from a review's record and its
// report.md (agent_review.go).

// reviewSARIF is the review as SARIF 2.1.0, for editors and code
// scanning pages: confirmed and uncertain findings only, each with what
// it assumes, since a result there reads as a demand otherwise.
func reviewSARIF(rv review) ([]byte, error) {
	levels := map[string]string{"critical": "error", "high": "error", "medium": "warning", "low": "note"}
	var rules []map[string]any
	for _, area := range rv.Areas {
		description := firstNonEmpty(reviewAreaBriefs[area], rv.Briefs[area])
		rules = append(rules, map[string]any{
			"id":               area,
			"name":             area,
			"shortDescription": map[string]string{"text": "hi agent review: " + area},
			"fullDescription":  map[string]string{"text": reviewLine(description, 1000)},
		})
	}
	results := []map[string]any{}
	for _, f := range rv.Findings {
		if f.Verdict != "confirmed" && f.Verdict != "uncertain" {
			continue
		}
		text := f.Title + ". " + firstNonEmpty(f.Failure, "")
		if f.Assumes != "" {
			text += " Assumes: " + f.Assumes
		}
		text += " (A recommendation from " + f.Agent + ", " + f.Verdict + " by a second agent; weigh it against how the app is run.)"
		result := map[string]any{
			"ruleId":  f.Area,
			"level":   firstNonEmpty(levels[f.severity()], "warning"),
			"message": map[string]string{"text": text},
			"locations": []map[string]any{{"physicalLocation": map[string]any{
				"artifactLocation": map[string]string{"uri": f.File, "uriBaseId": "%SRCROOT%"},
				"region":           map[string]int{"startLine": max(f.Start, 1), "endLine": max(f.End, f.Start, 1)},
			}}},
			"partialFingerprints": map[string]string{"hiAgentReview/v1": f.Fingerprint},
			"properties": map[string]any{"id": f.ID, "severity": f.severity(), "verdict": f.Verdict, "evidence": f.Evidence,
				"assumes": f.Assumes, "direction": f.Direction, "second_opinion": f.Reason, "reproduced": false},
		}
		results = append(results, result)
	}
	log := map[string]any{
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs": []map[string]any{{
			"tool": map[string]any{"driver": map[string]any{"name": "hi agent review", "version": version,
				"informationUri": "https://hi5.sh/guide/agent/review/", "rules": rules}},
			"versionControlProvenance": []map[string]any{{"repositoryUri": firstNonEmpty(rv.Source, "file://"+rv.Root), "revisionId": rv.Commit}},
			"results":                  results,
			"properties":               map[string]any{"review": rv.ID, "note": reviewHandoverLine},
		}},
	}
	return json.MarshalIndent(log, "", "  ")
}

func writeReviewSARIF(rv review) error {
	data, err := reviewSARIF(rv)
	if err != nil {
		return err
	}
	return os.WriteFile(reviewStateFile(rv.ID, "report.sarif"), append(data, '\n'), 0o600)
}

// reviewPage is report.html: one file, its styles inline, no scripts, and
// a policy that loads nothing, so it opens anywhere and stays private.
var reviewPage = template.Must(template.New("review").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">
<title>{{.Title}}</title>
<style>
:root { --bg: #fbfaf8; --text: #1f1d1a; --muted: #6b665e; --line: #e4e0d8; --code: #f1eee8; --accent: #8a4b14; --quote: #f6f1e7; }
@media (prefers-color-scheme: dark) {
  :root { --bg: #171614; --text: #ebe7e0; --muted: #a29c92; --line: #34312c; --code: #22201d; --accent: #e0a46a; --quote: #221f1a; }
}
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--text); font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
main { max-width: 52rem; margin: 0 auto; padding: 2rem 1rem 4rem; }
h1 { font-size: 1.7rem; line-height: 1.25; margin: 0 0 1.2rem; }
h2 { font-size: 1.2rem; line-height: 1.35; margin: 2.4rem 0 .8rem; padding-top: 1.2rem; border-top: 1px solid var(--line); }
a { color: var(--accent); }
blockquote { margin: 1rem 0; padding: .8rem 1rem; background: var(--quote); border-left: 3px solid var(--accent); border-radius: 4px; }
blockquote p { margin: .4rem 0; }
code { font: .88em/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; background: var(--code); padding: .1em .3em; border-radius: 3px; }
pre { background: var(--code); padding: .8rem 1rem; border-radius: 6px; overflow-x: auto; }
pre code { padding: 0; background: none; }
p code, li code { overflow-wrap: anywhere; }
ul { padding-left: 1.3rem; }
li { margin: .2rem 0; }
footer { margin-top: 3rem; color: var(--muted); font-size: .85rem; }
</style>
</head>
<body>
<main>
{{.Body}}
<footer>Written by hi agent review from report.md. Recommendations, not requirements.</footer>
</main>
</body>
</html>
`))

// writeReviewHTML renders report.md into report.html. Raw HTML in the
// markdown, which could come from a finding's text, isn't rendered.
func writeReviewHTML(rv review) (string, error) {
	source, err := os.ReadFile(reviewStateFile(rv.ID, "report.md"))
	if err != nil {
		return "", fmt.Errorf("%s has no report yet; it's written when the review ends (hi agent review watch %s)", rv.ID, rv.ID)
	}
	var body bytes.Buffer
	if err := goldmark.New(goldmark.WithExtensions(extension.GFM)).Convert(source, &body); err != nil {
		return "", err
	}
	var page bytes.Buffer
	title := fmt.Sprintf("Review %s: %s", rv.ID, reviewProjectName(rv))
	if err := reviewPage.Execute(&page, map[string]any{"Title": title, "Body": template.HTML(body.String())}); err != nil {
		return "", err
	}
	file := reviewStateFile(rv.ID, "report.html")
	if err := os.WriteFile(file, page.Bytes(), 0o600); err != nil {
		return "", err
	}
	return file, nil
}
