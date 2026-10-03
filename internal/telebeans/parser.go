package telebeans

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var inputPattern = regexp.MustCompile(`^\s*(?:(вчера)\s+)?(\S+)\s+(\S+)(?:\s+(.*))?\s*$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func ParseInput(text string, sent time.Time, cfg Config) (Draft, error) {
	d := Draft{Date: sent.In(cfg.Location).Format("2006-01-02"), Currency: cfg.DefaultCurrency}
	dateProvided := false
	words := strings.Fields(text)
	remaining := make([]string, 0, len(words))
	for _, word := range words {
		if !datePattern.MatchString(word) {
			remaining = append(remaining, word)
			continue
		}
		if dateProvided {
			return d, fmt.Errorf("укажите только одну дату")
		}
		if _, err := time.Parse("2006-01-02", word); err != nil {
			return d, fmt.Errorf("неверная дата: %s", word)
		}
		d.Date = word
		dateProvided = true
	}
	m := inputPattern.FindStringSubmatch(strings.Join(remaining, " "))
	if m == nil {
		return d, fmt.Errorf("формат: дома 350 хлеб и молоко")
	}
	if m[1] == "вчера" {
		if dateProvided {
			return d, fmt.Errorf("укажите только одну дату")
		}
		d.Date = sent.In(cfg.Location).AddDate(0, 0, -1).Format("2006-01-02")
	}
	n, err := ParseAmount(m[3])
	if err != nil {
		return d, err
	}
	account := m[2]
	if strings.EqualFold(account, "евро") || strings.EqualFold(account, "EUR") {
		return parseExchange(d, n, strings.Fields(m[4]), cfg)
	}
	for _, word := range strings.Fields(m[4]) {
		if word == "@@" {
			return d, fmt.Errorf("обмен: евро 500 @@ 59000")
		}
	}
	alias, known := cfg.Aliases[strings.ToLower(account)]
	if known {
		account = alias.Account
		d.Narration = alias.Narration
	} else if !strings.HasPrefix(account, "Expenses:") {
		d.UnknownAlias = account
		account = ""
		d.Narration = m[2]
	}
	d.Expenses = []Expense{{Account: account, Minor: n}}
	rest := strings.TrimSpace(m[4])
	first, tail, _ := strings.Cut(rest, " ")
	if currencyPattern.MatchString(first) {
		d.Currency = first
		rest = strings.TrimSpace(tail)
	}
	first, tail, _ = strings.Cut(rest, " ")
	if strings.HasPrefix(first, "@") {
		payment, ok := cfg.Payments[strings.TrimPrefix(first, "@")]
		if !ok {
			return d, fmt.Errorf("неизвестный способ оплаты: %s", first)
		}
		d.Payment = payment + ":" + d.Currency
		rest = strings.TrimSpace(tail)
	} else if d.Currency == cfg.DefaultCurrency {
		d.Payment = cfg.DefaultPayment
	}
	if rest != "" {
		d.Narration = rest
	}
	if d.Narration == "" {
		d.Narration = m[2]
	}
	return d, nil
}

func parseExchange(d Draft, received int64, words []string, cfg Config) (Draft, error) {
	if len(words) > 0 && words[0] == "EUR" {
		words = words[1:]
	}
	if len(words) < 2 || words[0] != "@@" {
		return d, fmt.Errorf("обмен: евро 500 @@ 59000 [описание]")
	}
	total, err := ParseAmount(words[1])
	if err != nil {
		return d, err
	}
	words = words[2:]
	if len(words) > 0 && currencyPattern.MatchString(words[0]) {
		if words[0] != "RSD" {
			return d, fmt.Errorf("покупка евро поддерживает оплату в RSD")
		}
		words = words[1:]
	}
	cash := cfg.Payments["нал"]
	if cash == "" {
		cash = "Assets:Cash"
	}
	d.Currency = "EUR"
	d.Payment = cash + ":RSD"
	d.Narration = "купил евро"
	d.Exchange = &Exchange{Account: cash + ":EUR", Minor: received, TotalMinor: total, TotalCurrency: "RSD"}
	if len(words) > 0 && strings.HasPrefix(words[0], "@") {
		payment, ok := cfg.Payments[strings.TrimPrefix(words[0], "@")]
		if !ok {
			return d, fmt.Errorf("неизвестный способ оплаты: %s", words[0])
		}
		d.Payment = payment + ":RSD"
		words = words[1:]
	}
	for _, word := range words {
		if word == "@@" {
			return d, fmt.Errorf("обмен: евро 500 [EUR] @@ 59000 [RSD] [@оплата] [описание]")
		}
	}
	if len(words) > 0 {
		d.Narration = strings.Join(words, " ")
	}
	return d, nil
}
