package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAppDevPickCluesOK(t *testing.T) {
	clues := appDevPickCluesOK([]appDevPickClue{
		{"id", "pay"}, {"text", "Pay"}, {"text", "pay"}, // the same, any case: once
		{"label", "ab"}, {"style", "color: red"}, {"text", "two\nlines"}, {"class", strings.Repeat("x", 101)},
		{"testid", " checkout-pay "},
	})
	var got []string
	for _, clue := range clues {
		got = append(got, clue.Kind+"="+clue.Text)
	}
	if strings.Join(got, " ") != "id=pay testid=checkout-pay" {
		t.Fatalf("%v", got)
	}
}

func TestAppDevPickRank(t *testing.T) {
	clues := []appDevPickClue{{"testid", "checkout-pay"}, {"text", "Pay now"}, {"class", "pay-button"}}
	matches := []appDevSearchMatch{
		{Path: "src/styles/pay.css", Line: 3, Text: ".pay-button { color: red }"},
		{Path: "src/Checkout.tsx", Line: 40, Text: `<button className="pay-button"`},
		{Path: "src/Checkout.tsx", Line: 41, Text: `  data-testid="checkout-pay">Pay now</button>`},
		{Path: "src/Checkout.test.tsx", Line: 9, Text: `getByTestId("checkout-pay"); getByText("Pay now")`},
		{Path: "README.md", Line: 1, Text: "Press Pay now"},
	}
	hits := appDevPickRank(matches, clues)
	var got []string
	for _, hit := range hits {
		got = append(got, hit.Path+":"+strconv.Itoa(hit.Line)+" "+strings.Join(hit.Clues, ","))
	}
	// Where it's made first, at the line with the most; tests and docs after.
	want := "src/Checkout.tsx:41 testid,text,class src/Checkout.test.tsx:9 testid,text README.md:1 text src/styles/pay.css:3 class"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestAppDevPickLocate(t *testing.T) {
	work := appTestLocalBox(t)
	os.MkdirAll(filepath.Join(work, "src"), 0o755)
	os.WriteFile(filepath.Join(work, "src", "Pay.jsx"), []byte("export function Pay() {\n  return <button id=\"pay\">Pay the order</button>;\n}\n"), 0o644)
	os.WriteFile(filepath.Join(work, "package-lock.json"), []byte(`{"pay": "Pay the order"}`), 0o644)
	hits, err := appDevPickLocate("payments", "w2", []appDevPickClue{{"id", "pay"}, {"text", "Pay the order"}, {"text", "--output=/tmp/x"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != "src/Pay.jsx" || hits[0].Line != 2 {
		t.Fatalf("%+v", hits)
	}
	if hits, err := appDevPickLocate("payments", "w2", nil); err != nil || len(hits) != 0 {
		t.Fatal(hits, err)
	}
	if _, err := appDevPickLocate("payments", "w9", []appDevPickClue{{"id", "pay"}}); err == nil {
		t.Fatal("a workspace that isn't")
	}
}
