package main

import (
	"os"
	"testing"
)

// teamTestBrainstormState writes hi agent brainstorm's record, as its own
// process would, with each done idea's response.
func teamTestBrainstormState(t *testing.T, bs brainstorm) {
	t.Helper()
	os.MkdirAll(brainstormStateFile(bs.ID), 0o700)
	if err := saveBrainstorm(&bs); err != nil {
		t.Fatal(err)
	}
	for _, run := range bs.Runs {
		if run.Kind == "idea" && run.State == "done" {
			os.MkdirAll(brainstormStateFile(bs.ID, "ideas", run.ID, "artifacts"), 0o700)
			os.WriteFile(brainstormStateFile(bs.ID, "ideas", run.ID, "response.md"), []byte("An idea from "+run.Name+"\n"), 0o600)
			for _, artifact := range run.Artifacts {
				os.WriteFile(brainstormStateFile(bs.ID, "ideas", run.ID, "artifacts", artifact), []byte("<svg/>"), 0o600)
			}
		}
	}
	if bs.State != "running" {
		os.WriteFile(brainstormStateFile(bs.ID, "question.md"), []byte(bs.Question+"\n"), 0o600)
		writeBrainstormReport(bs)
	}
}
