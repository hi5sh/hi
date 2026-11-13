package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The old names, hi server, hi connect, and hi disconnect, run what their
// hi server forms run, with the same output and exit code.
func TestOldNamesRunTheirOrgForms(t *testing.T) {
	ts := newTestServer(t)
	ts.connectAs(t, "anna", "staff")
	same := func(old, new []string) {
		t.Helper()
		oldCode, oldOut, oldErr := runHi(old...)
		newCode, newOut, newErr := runHi(new...)
		if oldCode != newCode || oldOut != newOut || oldErr != newErr {
			t.Errorf("hi %s and hi %s differ:\n%d %q %q\n%d %q %q", strings.Join(old, " "), strings.Join(new, " "),
				oldCode, oldOut, oldErr, newCode, newOut, newErr)
		}
	}
	same([]string{"server"}, []string{"org"})
	same([]string{"server", "--help"}, []string{"org", "help"})
	same([]string{"connect", "--help"}, []string{"org", "help"})
	same([]string{"server", "nonsense"}, []string{"org", "nonsense"})
	same([]string{"connect", "status"}, []string{"org", "status"})
	same([]string{"connect", "key"}, []string{"org", "key"})
	same([]string{"connect", "slack", "000000"}, []string{"org", "slack", "link", "000000"})
	same([]string{"server", "ls", "--dir", ts.dir}, []string{"org", "ls", "--dir", ts.dir})
	same([]string{"server", "requests", "--dir", ts.dir}, []string{"org", "requests", "--dir", ts.dir})
	same([]string{"connect"}, []string{"org", "join", "a", "b"})
	same([]string{"disconnect", "now"}, []string{"org", "leave", "now"})

	if code, stdout, _ := runHi("org", "leave"); code != 0 || !strings.Contains(stdout, "Disconnected from "+ts.url) {
		t.Fatalf("hi server leave: %d %s", code, stdout)
	}
	same([]string{"disconnect"}, []string{"org", "leave"})
	if code, stdout, stderr := runHi("connect", ts.url, "--user", "anna", "--no-wait"); code != exitPending || !strings.Contains(stdout, "hi server approve e-") {
		t.Fatalf("hi connect still joins: %d %s%s", code, stdout, stderr)
	}
}

func TestHelpNamesOnlyServer(t *testing.T) {
	_, stdout, _ := runHi("help")
	if !strings.Contains(stdout, "\n  hi server ") {
		t.Errorf("hi help doesn't list hi server:\n%s", stdout)
	}
	for _, old := range []string{"hi org", "hi connect", "hi disconnect"} {
		if strings.Contains(stdout, old) {
			t.Errorf("hi help still lists %s", old)
		}
	}
}

// No message names the old commands: usage, errors, hints, and what goes to
// Slack all say hi server (hi_server_apps.md). Comments may keep the old
// words, and hi server's help says once that hi org is its old name.
func TestMessagesUseServerNames(t *testing.T) {
	old := regexp.MustCompile(`\bhi (org|connect|disconnect)\b`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if match := old.FindString(lit.Value); match != "" && !strings.Contains(lit.Value, "hi org is its old name") && !strings.Contains(lit.Value, "Description=hi org server") {
					t.Errorf("%s: %q; say hi server", fset.Position(lit.Pos()), match)
				}
			}
			return true
		})
	}
}
