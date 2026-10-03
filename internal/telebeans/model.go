package telebeans

import (
	"fmt"
	"strconv"
	"strings"
)

type Alias struct {
	Account   string `json:"account"`
	Narration string `json:"narration"`
}

type Posting struct {
	Account string         `json:"account"`
	Amount  string         `json:"amount"`
	Meta    map[string]any `json:"meta,omitempty"`
}

type Transaction struct {
	Type      string         `json:"t"`
	Date      string         `json:"date"`
	Flag      string         `json:"flag"`
	Payee     string         `json:"payee"`
	Narration string         `json:"narration"`
	Tags      []string       `json:"tags"`
	Links     []string       `json:"links"`
	Meta      map[string]any `json:"meta"`
	Postings  []Posting      `json:"postings"`
}

type Expense struct {
	Account string `json:"account"`
	Minor   int64  `json:"minor"`
}

type Draft struct {
	Date         string    `json:"date"`
	Narration    string    `json:"narration"`
	Currency     string    `json:"currency"`
	Payment      string    `json:"payment"`
	UnknownAlias string    `json:"unknown_alias,omitempty"`
	Expenses     []Expense `json:"expenses"`
}

func ParseAmount(s string) (int64, error) {
	s = strings.ReplaceAll(strings.TrimSuffix(s, "."), ",", ".")
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("неверная сумма: %s", s)
	}
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("неверная сумма: %s", s)
		}
	}
	frac := "00"
	if len(parts) == 2 {
		if len(parts[1]) < 1 || len(parts[1]) > 2 {
			return 0, fmt.Errorf("в сумме допустимы два знака после запятой")
		}
		for _, c := range parts[1] {
			if c < '0' || c > '9' {
				return 0, fmt.Errorf("неверная сумма: %s", s)
			}
		}
		frac = parts[1] + strings.Repeat("0", 2-len(parts[1]))
	}
	n, err := strconv.ParseInt(parts[0]+frac, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("сумма должна быть положительной и помещаться в int64")
	}
	return n, nil
}

func FormatAmount(n int64) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	if n%100 == 0 {
		return fmt.Sprintf("%s%d", sign, n/100)
	}
	return fmt.Sprintf("%s%d.%02d", sign, n/100, n%100)
}

func (d Draft) Transaction(id string) (Transaction, error) {
	t := Transaction{Type: "Transaction", Date: d.Date, Flag: "*", Narration: d.Narration, Tags: []string{}, Links: []string{}, Meta: map[string]any{"telebeans_id": id}, Postings: []Posting{}}
	var total int64
	if d.Payment == "" || len(d.Expenses) == 0 {
		return t, fmt.Errorf("нужно выбрать счета")
	}
	for _, e := range d.Expenses {
		if e.Account == "" || e.Minor <= 0 || e.Minor > (1<<63-1)-total {
			return t, fmt.Errorf("неверная сумма или счёт")
		}
		total += e.Minor
		t.Postings = append(t.Postings, Posting{Account: e.Account, Amount: FormatAmount(e.Minor) + " " + d.Currency})
	}
	t.Postings = append(t.Postings, Posting{Account: d.Payment, Amount: FormatAmount(-total) + " " + d.Currency})
	return t, nil
}

func (t Transaction) ID() string { id, _ := t.Meta["telebeans_id"].(string); return id }
