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

func TestExchangeSourcePreservesTotalAndComments(t *testing.T) {
	d := Draft{Date: "2026-09-12", Narration: "купил евро", Currency: "EUR", Payment: "Assets:Cash:RSD", Exchange: &Exchange{Account: "Assets:Cash:EUR", Minor: 50000, TotalMinor: 5900000, TotalCurrency: "RSD"}}
	txn, err := d.Transaction("exchange")
	if err != nil {
		t.Fatal(err)
	}
	// Fava returns a normalized unit price, even though the source uses @@.
	txn.Postings[0].Amount = "500 EUR @ 118 RSD"
	source := `2026-09-12 * "купил евро"
  telebeans_id: "exchange"
  note: "kept in Fava"
  Assets:Cash:EUR  500 EUR @@ 59000 RSD ; rate @ comment
    note: "received"
  Assets:Cash:RSD ; paid cash
`
	context := EntryContext{Entry: txn, Source: source}
	parsed, err := DraftFromContext(context)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Exchange == nil || parsed.Exchange.TotalMinor != 5900000 {
		t.Fatalf("lost total: %+v", parsed)
	}
	parsed.Date = "2026-09-11"
	parsed.Narration = "обмен"
	parsed.Payment = "Assets:Raif:RSD"
	parsed.Exchange.Minor = 300
	parsed.Exchange.TotalMinor = 10000
	patched, err := patchSource(context, parsed)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`2026-09-11 * "обмен"`, `note: "kept in Fava"`, `Assets:Cash:EUR  3 EUR @@ 100 RSD ; rate @ comment`, `note: "received"`, `Assets:Raif:RSD ; paid cash`} {
		if !strings.Contains(patched, part) {
			t.Fatalf("missing %s in:\n%s", part, patched)
		}
	}
	// Reading the declared total avoids rounding a repeating unit price or inferred debit.
	txn.Date = parsed.Date
	txn.Narration = parsed.Narration
	txn.Postings = []Posting{{Account: "Assets:Cash:EUR", Amount: "3 EUR @ 33.33333333333333333333333333 RSD"}, {Account: "Assets:Raif:RSD", Amount: "-99.99999999999999999999999999 RSD"}}
	read, err := DraftFromContext(EntryContext{Entry: txn, Source: patched})
	if err != nil || read.Exchange == nil || read.Exchange.Minor != 300 || read.Exchange.TotalMinor != 10000 {
		t.Fatalf("repeating rate changed total: %+v %v", read, err)
	}
}
