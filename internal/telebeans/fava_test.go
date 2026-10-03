package telebeans

import (
	"strings"
	"testing"
)

func TestPatchPreservesSource(t *testing.T) {
	d := Draft{Date: "2026-10-03", Narration: "Еда", Currency: "RSD", Payment: "Assets:Raif:RSD", Expenses: []Expense{{"Expenses:Жизнь:Едим:Дома", 35000}}}
	entry, err := d.Transaction("test")
	if err != nil {
		t.Fatal(err)
	}
	source := `2026-10-03 * "" "Еда" #groceries ; "header comment"
  telebeans_id: "test"
  note: "added in Fava"
  Expenses:Жизнь:Едим:Дома  350 RSD ; @ a comment
    note: "posting comment"
  Assets:Raif:RSD          -350 RSD
`
	d.Date = "2026-10-02"
	d.Narration = "хлеб и \"молоко\""
	d.Payment = "Assets:Cash:RSD"
	d.Expenses[0].Minor = 35050
	patched, err := patchSource(EntryContext{Entry: entry, Source: source}, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`2026-10-02 * "" "хлеб и \"молоко\"" #groceries ; "header comment"`, `note: "added in Fava"`, `note: "posting comment"`, `350.50 RSD ; @ a comment`, `Assets:Cash:RSD          -350.50 RSD`} {
		if !strings.Contains(patched, part) {
			t.Fatalf("missing %s in:\n%s", part, patched)
		}
	}
}
