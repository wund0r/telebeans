package telebeans

import (
	"math"
	"testing"
	"time"
)

func testConfig() Config {
	location, _ := time.LoadLocation("Europe/Belgrade")
	return Config{Location: location, DefaultCurrency: "RSD", DefaultPayment: "Assets:Raif:RSD", UserID: 123,
		Aliases: map[string]Alias{"дома": {"Expenses:Жизнь:Едим:Дома", "Еда"}, "кафе": {"Expenses:Жизнь:Едим:Кафе", "Кафе"}, "аренда": {"Expenses:Дом:Аренда", "Аренда"}}, Payments: map[string]string{"нал": "Assets:Cash", "raif": "Assets:Raif"}}
}
func TestAmounts(t *testing.T) {
	for _, tc := range []struct {
		input     string
		minor     int64
		formatted string
	}{{"350", 35000, "350"}, {"350.", 35000, "350"}, {"0,01", 1, "0.01"}, {"1.5", 150, "1.50"}, {"92233720368547758.07", math.MaxInt64, "92233720368547758.07"}} {
		n, err := ParseAmount(tc.input)
		if err != nil || n != tc.minor || FormatAmount(n) != tc.formatted {
			t.Fatalf("%s: %d, %v", tc.input, n, err)
		}
	}
	for _, s := range []string{"0", "-1", "NaN", "1e3", "1.001", "92233720368547758.08", "1,2,3", ".5"} {
		if _, err := ParseAmount(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
	d := Draft{Payment: "Assets:Raif:RSD", Currency: "RSD", Expenses: []Expense{{"Expenses:A", math.MaxInt64}, {"Expenses:B", 1}}}
	if _, err := d.Transaction("overflow"); err == nil {
		t.Fatal("sum overflow accepted")
	}
}
func TestParseInput(t *testing.T) {
	// UTC day differs from the user's local date.
	sent := time.Date(2026, 10, 2, 23, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		input, date, account, payment, currency, narration, unknown string
		minor                                                       int64
	}{
		{"дома 350", "2026-10-03", "Expenses:Жизнь:Едим:Дома", "Assets:Raif:RSD", "RSD", "Еда", "", 35000},
		{"дома 350 @нал хлеб и молоко", "2026-10-03", "Expenses:Жизнь:Едим:Дома", "Assets:Cash:RSD", "RSD", "хлеб и молоко", "", 35000},
		{"вчера кафе 500. блины", "2026-10-02", "Expenses:Жизнь:Едим:Кафе", "Assets:Raif:RSD", "RSD", "блины", "", 50000},
		{"2026-08-29 аренда 400 EUR за август", "2026-08-29", "Expenses:Дом:Аренда", "", "EUR", "за август", "", 40000},
		{"дома 350 2026-09-30", "2026-09-30", "Expenses:Жизнь:Едим:Дома", "Assets:Raif:RSD", "RSD", "Еда", "", 35000},
		{"дома 2026-09-30 350 хлеб и молоко", "2026-09-30", "Expenses:Жизнь:Едим:Дома", "Assets:Raif:RSD", "RSD", "хлеб и молоко", "", 35000},
		{"дома 350 @нал хлеб 2026-09-30 и молоко", "2026-09-30", "Expenses:Жизнь:Едим:Дома", "Assets:Cash:RSD", "RSD", "хлеб и молоко", "", 35000},
		{"аренда 400 EUR 2026-08-29 за август", "2026-08-29", "Expenses:Дом:Аренда", "", "EUR", "за август", "", 40000},
		{"дома 350 хлеб и молоко 2026-09-30", "2026-09-30", "Expenses:Жизнь:Едим:Дома", "Assets:Raif:RSD", "RSD", "хлеб и молоко", "", 35000},
		{"дома 350 2024-02-29", "2024-02-29", "Expenses:Жизнь:Едим:Дома", "Assets:Raif:RSD", "RSD", "Еда", "", 35000},
		{"блины 350,50", "2026-10-03", "", "Assets:Raif:RSD", "RSD", "блины", "блины", 35050},
	} {
		d, err := ParseInput(tc.input, sent, testConfig())
		if err != nil {
			t.Fatalf("%s: %v", tc.input, err)
		}
		if d.Date != tc.date || d.Payment != tc.payment || d.Currency != tc.currency || d.Narration != tc.narration || d.UnknownAlias != tc.unknown || d.Expenses[0].Account != tc.account || d.Expenses[0].Minor != tc.minor {
			t.Fatalf("%s: %+v", tc.input, d)
		}
	}
	for _, s := range []string{"дома", "дома -350", "2026-02-30 дома 350", "дома 350 @nope", "дома 350 2026-02-29", "дома 350 хлеб 2026-13-01", "2026-09-30 дома 350 2026-10-01", "дома 350 2026-09-30 2026-09-30", "вчера дома 350 2026-09-30"} {
		if _, err := ParseInput(s, sent, testConfig()); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestParseEuroPurchase(t *testing.T) {
	sent := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		input, date, payment, narration string
		received, total                 int64
	}{
		{"2026-09-12 евро 500 @@ 59000", "2026-09-12", "Assets:Cash:RSD", "купил евро", 50000, 5900000},
		{"евро 500 @@ 58900 2026-09-12", "2026-09-12", "Assets:Cash:RSD", "купил евро", 50000, 5890000},
		{"EUR 500.50 EUR @@ 58900,25 RSD @raif обмен валюты", "2026-10-03", "Assets:Raif:RSD", "обмен валюты", 50050, 5890025},
		{"евро 500 @@ 59000 RSD @raif для ATM", "2026-10-03", "Assets:Raif:RSD", "для ATM", 50000, 5900000},
		{"вчера евро 3 @@ 100", "2026-10-02", "Assets:Cash:RSD", "купил евро", 300, 10000},
	} {
		d, err := ParseInput(tc.input, sent, testConfig())
		if err != nil {
			t.Fatalf("%s: %v", tc.input, err)
		}
		if d.Date != tc.date || d.Currency != "EUR" || d.Payment != tc.payment || d.Narration != tc.narration || len(d.Expenses) != 0 || d.UnknownAlias != "" || d.Exchange == nil || d.Exchange.Account != "Assets:Cash:EUR" || d.Exchange.Minor != tc.received || d.Exchange.TotalMinor != tc.total || d.Exchange.TotalCurrency != "RSD" {
			t.Fatalf("%s: %+v", tc.input, d)
		}
		txn, err := d.Transaction("exchange")
		if err != nil {
			t.Fatal(err)
		}
		if txn.Postings[0].Amount != FormatAmount(tc.received)+" EUR @@ "+FormatAmount(tc.total)+" RSD" || txn.Postings[1].Amount != FormatAmount(-tc.total)+" RSD" {
			t.Fatalf("wrong purchase totals: %+v", txn.Postings)
		}
	}
	for _, input := range []string{"евро 500", "евро 500 @ 59000", "евро 500 @@", "евро 500 @@ -59000", "евро 500 @@ 0", "евро 0 @@ 59000", "евро 500 @@ 59000.001", "евро 500 @@ 59000 USD", "евро 500 @@ 59000 @@ 500", "кафе 500 @@ 59000"} {
		if _, err := ParseInput(input, sent, testConfig()); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	// Account shortcuts remain local configuration; cash names aren't fixed in code.
	cfg := testConfig()
	cfg.Payments["нал"] = "Assets:Wallet"
	d, err := ParseInput("евро 500 @@ 59000", sent, cfg)
	if err != nil || d.Payment != "Assets:Wallet:RSD" || d.Exchange == nil || d.Exchange.Account != "Assets:Wallet:EUR" {
		t.Fatalf("cash shortcut: %+v %v", d, err)
	}
}
