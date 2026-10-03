package telebeans

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Fava struct {
	URL  string
	HTTP *http.Client
}
type Ledger struct {
	Accounts       []string `json:"accounts"`
	Currencies     []string `json:"currencies"`
	AccountDetails map[string]struct {
		CloseDate *string `json:"close_date"`
	} `json:"account_details"`
	Options struct {
		Filename            string   `json:"filename"`
		OperatingCurrencies []string `json:"operating_currency"`
	} `json:"options"`
	FavaOptions struct {
		DefaultFile string `json:"default_file"`
	} `json:"fava_options"`
}
type EntryContext struct {
	Entry    Transaction `json:"entry"`
	Source   string      `json:"slice"`
	Checksum string      `json:"sha256sum"`
	Hash     string      `json:"-"`
}

func NewFava(base string) *Fava {
	return &Fava{strings.TrimRight(base, "/"), &http.Client{Timeout: 30 * time.Second}}
}

func (f *Fava) call(ctx context.Context, method, endpoint string, query url.Values, body, out any) error {
	var data io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		data = bytes.NewReader(b)
	}
	u := f.URL + "/api/" + endpoint
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, data)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Fava %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Error  string          `json:"error"`
		Detail string          `json:"detail"`
	}
	if err = json.Unmarshal(b, &envelope); err != nil {
		return fmt.Errorf("Fava %s: HTTP %d, expected JSON", endpoint, resp.StatusCode)
	}
	if resp.StatusCode >= 400 || envelope.Error != "" {
		return fmt.Errorf("Fava %s: HTTP %d %s %s", endpoint, resp.StatusCode, envelope.Error, envelope.Detail)
	}
	if out != nil {
		if err = json.Unmarshal(envelope.Data, out); err != nil {
			return fmt.Errorf("Fava %s response: %w", endpoint, err)
		}
	}
	return nil
}

func (f *Fava) Refresh(ctx context.Context) error {
	return f.call(ctx, "GET", "changed", nil, nil, nil)
}
func (f *Fava) Ledger(ctx context.Context) (Ledger, error) {
	var l Ledger
	if err := f.Refresh(ctx); err != nil {
		return l, err
	}
	err := f.call(ctx, "GET", "ledger_data", nil, nil, &l)
	return l, err
}
func (l Ledger) Choices(prefix, currency, date string) []string {
	var result []string
	for _, a := range l.Accounts {
		if !strings.HasPrefix(a, prefix) {
			continue
		}
		if currency != "" && !strings.HasSuffix(a, ":"+currency) {
			continue
		}
		if close := l.AccountDetails[a].CloseDate; close != nil && *close <= date {
			continue
		}
		result = append(result, a)
	}
	sort.Strings(result)
	return result
}
func (l Ledger) Validate(d Draft) error {
	validCurrency := false
	for _, c := range append(append([]string{}, l.Currencies...), l.Options.OperatingCurrencies...) {
		if c == d.Currency {
			validCurrency = true
		}
	}
	if !validCurrency {
		return fmt.Errorf("неизвестная валюта: %s", d.Currency)
	}
	for _, a := range appendExpenseAccounts(d) {
		found := false
		for _, candidate := range l.Accounts {
			if a == candidate {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("счёт отсутствует в Fava: %s", a)
		}
		if close := l.AccountDetails[a].CloseDate; close != nil && *close <= d.Date {
			return fmt.Errorf("счёт закрыт: %s", a)
		}
	}
	return nil
}
func appendExpenseAccounts(d Draft) []string {
	a := []string{d.Payment}
	for _, e := range d.Expenses {
		a = append(a, e.Account)
	}
	return a
}

// A stable metadata ID survives edits, unlike Fava's entry hash.
func (f *Fava) Find(ctx context.Context, id string) (*EntryContext, error) {
	if err := f.Refresh(ctx); err != nil {
		return nil, err
	}
	q := "SELECT DISTINCT id WHERE entry_meta('telebeans_id') = '" + strings.ReplaceAll(id, "'", "''") + "'"
	var result struct {
		Rows [][]string `json:"rows"`
	}
	if err := f.call(ctx, "GET", "query", url.Values{"query_string": {q}}, nil, &result); err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, nil
	}
	if len(result.Rows) != 1 || len(result.Rows[0]) != 1 {
		return nil, fmt.Errorf("duplicate telebeans_id in ledger: %s", id)
	}
	var entry EntryContext
	entry.Hash = result.Rows[0][0]
	if err := f.call(ctx, "GET", "context", url.Values{"entry_hash": {entry.Hash}}, nil, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}
func (f *Fava) Add(ctx context.Context, d Draft, id string) (*EntryContext, error) {
	existing, err := f.Find(ctx, id)
	if err != nil || existing != nil {
		return existing, err
	}
	l, err := f.Ledger(ctx)
	if err != nil {
		return nil, err
	}
	if err = l.Validate(d); err != nil {
		return nil, err
	}
	t, err := d.Transaction(id)
	if err != nil {
		return nil, err
	}
	if err = f.call(ctx, "PUT", "add_entries", nil, map[string]any{"entries": []Transaction{t}}, nil); err != nil {
		return nil, err
	}
	e, err := f.Find(ctx, id)
	if err == nil && e == nil {
		err = fmt.Errorf("Fava accepted the entry, but it is not visible in the ledger; check Fava before retrying")
	}
	return e, err
}
func (f *Fava) Delete(ctx context.Context, id string) error {
	e, err := f.Find(ctx, id)
	if err != nil || e == nil {
		return err
	}
	return f.call(ctx, "DELETE", "source_slice", url.Values{"entry_hash": {e.Hash}, "sha256sum": {e.Checksum}}, nil, nil)
}
func (f *Fava) Edit(ctx context.Context, id string, d Draft) (*EntryContext, error) {
	e, err := f.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, fmt.Errorf("транзакция уже удалена")
	}
	l, err := f.Ledger(ctx)
	if err != nil {
		return nil, err
	}
	if err = l.Validate(d); err != nil {
		return nil, err
	}
	source, err := patchSource(*e, d)
	if err != nil {
		return nil, err
	}
	err = f.call(ctx, "PUT", "source_slice", nil, map[string]string{"entry_hash": e.Hash, "sha256sum": e.Checksum, "source": source}, nil)
	if err != nil {
		return nil, err
	}
	return f.Find(ctx, id)
}

func DraftFromEntry(t Transaction) (Draft, error) {
	d := Draft{Date: t.Date, Narration: t.Narration}
	for _, p := range t.Postings {
		parts := strings.Fields(p.Amount)
		if len(parts) != 2 {
			return d, fmt.Errorf("редактирование поддерживает только простые проводки")
		}
		if d.Currency != "" && d.Currency != parts[1] {
			return d, fmt.Errorf("редактирование разных валют пока не поддерживается")
		}
		d.Currency = parts[1]
		negative := strings.HasPrefix(parts[0], "-")
		n, err := ParseAmount(strings.TrimPrefix(parts[0], "-"))
		if err != nil {
			return d, err
		}
		if negative && strings.HasPrefix(p.Account, "Assets:") && d.Payment == "" {
			d.Payment = p.Account
		} else if !negative && strings.HasPrefix(p.Account, "Expenses:") {
			d.Expenses = append(d.Expenses, Expense{p.Account, n})
		} else {
			return d, fmt.Errorf("неподдерживаемые проводки в транзакции")
		}
	}
	if d.Payment == "" || len(d.Expenses) == 0 {
		return d, fmt.Errorf("неподдерживаемые проводки в транзакции")
	}
	return d, nil
}

var transactionHeader = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\s+\S+\s+(?:"(?:\\.|[^"\\])*"\s+)?("(?:\\.|[^"\\])*")`)
var postingLine = regexp.MustCompile(`^(\s+)(\S+)(\s+)(-?\d+(?:\.\d+)?)(\s+)(\S+)(.*)$`)

// Patch only the header and posting values. Keep the ledger's metadata and comments.
func patchSource(e EntryContext, d Draft) (string, error) {
	original, err := DraftFromEntry(e.Entry)
	if err != nil {
		return "", err
	}
	t, err := d.Transaction(e.Entry.ID())
	if err != nil {
		return "", err
	}
	if len(t.Postings) != len(e.Entry.Postings) {
		return "", fmt.Errorf("нельзя менять число проводок")
	}
	lines := strings.Split(e.Source, "\n")
	if len(lines) == 0 || len(lines[0]) < 10 {
		return "", fmt.Errorf("неподдерживаемый формат записи Fava")
	}
	lines[0] = d.Date + lines[0][10:]
	if original.Narration != d.Narration {
		match := transactionHeader.FindStringSubmatchIndex(lines[0])
		if len(match) != 4 {
			return "", fmt.Errorf("не найдено описание в записи")
		}
		lines[0] = lines[0][:match[2]] + strconv.Quote(d.Narration) + lines[0][match[3]:]
	}
	index := 0
	for i := 1; i < len(lines); i++ {
		m := postingLine.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		if index >= len(t.Postings) || m[2] != e.Entry.Postings[index].Account {
			return "", fmt.Errorf("проводки изменились вне бота; проверьте запись в Fava")
		}
		annotation, _, _ := strings.Cut(m[7], ";")
		if strings.ContainsAny(annotation, "{@") {
			return "", fmt.Errorf("редактирование стоимости и курса пока не поддерживается")
		}
		p := t.Postings[index]
		amount, currency, _ := strings.Cut(p.Amount, " ")
		lines[i] = m[1] + p.Account + m[3] + amount + m[5] + currency + m[7]
		index++
	}
	if index != len(t.Postings) {
		return "", fmt.Errorf("не удалось прочитать все проводки Fava")
	}
	return strings.Join(lines, "\n"), nil
}
