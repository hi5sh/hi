package cli

import (
	"strings"
	"testing"
)

// A fork takes the conversation as it is now, to the end of the response's
// turn, under the new session id: not a branch a rewind left, not the
// turns after.
func TestAppDevFork(t *testing.T) {
	line := func(uuid, parent, kind, content string) string {
		return `{"uuid":"` + uuid + `","parentUuid":"` + parent + `","type":"` + kind + `","sessionId":"old","message":{"content":` + content + `}}`
	}
	transcript := strings.Join([]string{
		line("u1", "", "user", `"Make it green"`),
		line("a1", "u1", "assistant", `[{"type":"text","text":"Done."}]`),
		line("u2", "a1", "user", `"Make it red"`), // rewound: a branch left behind
		line("a2", "u2", "assistant", `[{"type":"text","text":"Red."}]`),
		`{"type":"file-history-snapshot","messageId":"u3","snapshot":{}}`,
		line("u3", "a1", "user", `"Add a legend"`),
		line("a3", "u3", "assistant", `[{"type":"tool_use","id":"t1","name":"Read","input":{}}]`),
		line("r3", "a3", "user", `[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]`),
		line("a4", "r3", "assistant", `[{"type":"text","text":"Added the legend."}]`),
		line("u5", "a4", "user", `"Thanks"`),
		line("a5", "u5", "assistant", `[{"type":"text","text":"You're welcome."}]`),
	}, "\n") + "\n"
	got, err := appDevFork([]byte(transcript), "old", "new", "a3")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, l := range strings.Split(strings.TrimSpace(string(got)), "\n") {
		if strings.Contains(l, `"sessionId":"old"`) {
			t.Fatal("the old session id stays:", l)
		}
		if i := strings.Index(l, `"uuid":"`); i >= 0 {
			ids = append(ids, l[i+8:i+10])
		} else {
			ids = append(ids, "snapshot")
		}
	}
	if strings.Join(ids, " ") != "u1 a1 snapshot u3 a3 r3 a4" {
		t.Fatal(ids)
	}
	// The last response: the whole conversation.
	if got, _ := appDevFork([]byte(transcript), "old", "new", "a5"); strings.Count(string(got), "\n") != 9 {
		t.Fatal(string(got))
	}
	// A response a rewind left: not in the conversation now.
	if _, err := appDevFork([]byte(transcript), "old", "new", "a2"); err == nil {
		t.Fatal("forked from a branch the conversation left")
	}
	if id := appDevNewSessionID(); len(id) != 36 || id[14] != '4' {
		t.Fatal(id)
	}
}
