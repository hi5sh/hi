package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// --compare: what an earlier review found, and what became of it
// (agent_review.go).

// reviewCompare is the comparison with an earlier review.
type reviewCompare struct {
	From       string              `json:"from"` // the earlier review's id, or its report's path
	FromID     string              `json:"from_id"`
	FromCommit string              `json:"from_commit"`
	FromDate   time.Time           `json:"from_date"`
	Items      []reviewCompareItem `json:"items"`
}

// reviewCompareItem is one finding of the earlier review.
type reviewCompareItem struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Area    string `json:"area"`
	File    string `json:"file"`
	Verdict string `json:"verdict"`
	// Status is "still seen", "not seen, code changed" (the quoted lines
	// aren't there now), or "not seen, code unchanged" (they are, and no
	// investigator raised it).
	Status string `json:"status"`
	Now    string `json:"now,omitempty"` // the finding it is now
	// FixedBy is the work that set out to fix it, from --fixes.
	FixedBy string `json:"fixed_by,omitempty"`
}

// loadReviewCompare reads the earlier review: one of yours by its id, or a
// report.json from anywhere.
func loadReviewCompare(from string) (review, error) {
	var earlier review
	path := from
	if !fileExists(path) {
		if _, err := loadReview(from); err != nil {
			return earlier, fmt.Errorf("--compare takes a review's id or a report.json; %s is neither", from)
		}
		path = reviewStateFile(from, "report.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return earlier, fmt.Errorf("%s has no report yet; compare with a review that has ended", from)
	}
	if err := json.Unmarshal(data, &earlier); err != nil || earlier.ID == "" {
		return earlier, fmt.Errorf("%s isn't a hi agent review report", from)
	}
	for _, f := range earlier.Findings {
		if strings.HasPrefix(f.ID, "c-") {
			return earlier, fmt.Errorf("%s hasn't ended; compare with a review that has", from)
		}
	}
	return earlier, nil
}

// compareReview matches the earlier review's findings with this one's: by
// fingerprint, or by place, where the earlier finding's code is now and a
// finding of this review covers those lines. A finding not seen isn't
// called fixed: either its code changed, or nobody raised it this time.
func compareReview(rv *review) {
	if rv.Compare == nil {
		return
	}
	data, err := os.ReadFile(reviewStateFile(rv.ID, "compare.json"))
	if err != nil {
		return
	}
	var earlier review
	if json.Unmarshal(data, &earlier) != nil {
		return
	}
	primary := func(f reviewFinding) string { return firstNonEmpty(f.DuplicateOf, f.ID) }
	byID := map[string]*reviewFinding{}
	for i := range rv.Findings {
		rv.Findings[i].Earlier = ""
		byID[rv.Findings[i].ID] = &rv.Findings[i]
	}
	rv.Compare.Items = []reviewCompareItem{}
	for _, e := range earlier.Findings {
		if e.Verdict == "duplicate" {
			continue
		}
		item := reviewCompareItem{ID: e.ID, Title: e.Title, Area: e.Area, File: e.File, Verdict: e.Verdict, FixedBy: rv.Fixes[e.Fingerprint]}
		for _, f := range rv.Findings {
			if f.Fingerprint != "" && f.Fingerprint == e.Fingerprint {
				item.Now = primary(f)
				break
			}
		}
		if item.Now == "" {
			code := reviewQuoteLines(firstNonEmpty(e.Code, e.Quote))
			start, end, found := reviewLocate(reviewFileLines(rv.Root, rv.Commit, e.File), code)
			if !found {
				item.Status = "not seen, code changed"
			} else {
				for _, f := range rv.Findings {
					if f.File == e.File && f.Evidence != "missing" && f.Start <= end && start <= f.End {
						item.Now = primary(f)
						break
					}
				}
				if item.Now == "" {
					item.Status = "not seen, code unchanged"
				}
			}
		}
		if item.Now != "" {
			item.Status = "still seen"
			if f := byID[item.Now]; f != nil && f.Earlier == "" {
				f.Earlier = e.ID
			}
		}
		rv.Compare.Items = append(rv.Compare.Items, item)
	}
}

// reviewLocate finds code's lines, compared without indentation, in a
// file's lines: the first and last line numbers.
func reviewLocate(lines, code []string) (int, int, bool) {
	if len(code) == 0 || lines == nil {
		return 0, 0, false
	}
	for start := 0; start+len(code) <= len(lines); start++ {
		match := true
		for i, want := range code {
			if strings.TrimSpace(lines[start+i]) != want {
				match = false
				break
			}
		}
		if match {
			return start + 1, start + len(code), true
		}
	}
	return 0, 0, false
}

// reviewCompareCounts is the comparison in one line.
func reviewCompareCounts(rv review) string {
	if rv.Compare == nil {
		return ""
	}
	counts := map[string]int{}
	for _, item := range rv.Compare.Items {
		counts[item.Status]++
	}
	fresh := 0
	for _, f := range rv.Findings {
		if f.Earlier == "" && f.Verdict != "duplicate" && f.Verdict != "rejected" {
			fresh++
		}
	}
	return fmt.Sprintf("since %s: %d new, %d still seen, %d not seen with the code changed, %d not seen with the code unchanged",
		rv.Compare.FromID, fresh, counts["still seen"], counts["not seen, code changed"], counts["not seen, code unchanged"])
}

// reviewCompareMarkdown is the report's section on the comparison.
func reviewCompareMarkdown(rv review) string {
	c := rv.Compare
	if c == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n## Compared with %s\n\n%s (%s, at `%s`): %s.\n\n", c.FromID, c.FromID, c.FromDate.Format("2006-01-02"), shortCommit(c.FromCommit),
		strings.TrimPrefix(reviewCompareCounts(rv), "since "+c.FromID+": "))
	b.WriteString("A finding not seen this time isn't called fixed. When its code changed, it may have been fixed or moved; when its code is unchanged, the agents may simply have missed it this time, so it's worth a look.\n\n")
	for _, status := range []string{"still seen", "not seen, code changed", "not seen, code unchanged"} {
		var lines []string
		for _, item := range c.Items {
			if item.Status != status {
				continue
			}
			line := fmt.Sprintf("- %s · %s (`%s`, %s, was %s)", item.ID, item.Title, item.File, item.Area, item.Verdict)
			switch {
			case item.Now != "":
				line += ": now " + item.Now
				if item.FixedBy != "" {
					line += fmt.Sprintf(", though %s set out to fix it", item.FixedBy)
				}
			case item.FixedBy != "":
				line += ": not seen since " + item.FixedBy
			}
			lines = append(lines, line)
		}
		if len(lines) > 0 {
			fmt.Fprintf(&b, "**%s%s**\n\n%s\n\n", strings.ToUpper(status[:1]), status[1:], strings.Join(lines, "\n"))
		}
	}
	return b.String()
}
