package telebeans

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testFavaBinary = flag.String("fava-test-binary", os.Getenv("TELEBEANS_TEST_FAVA"), "Fava executable for integration tests")

const fixture = `option "title" "Test"
option "operating_currency" "RSD"
option "operating_currency" "EUR"
include "current.beancount"
`
const currentFixture = `2020-01-01 custom "fava-option" "default-file" "current.beancount"
2020-01-01 open Assets:Raif:RSD RSD
2020-01-01 open Assets:Cash:RSD RSD
2020-01-01 open Assets:Cash:EUR EUR
2020-01-01 open Assets:Raif:EUR EUR
2020-01-01 open Expenses:Жизнь:Едим:Дома
2020-01-01 open Expenses:Жизнь:Едим:Кафе
2020-01-01 open Expenses:Дом:Аренда
`

func disposableFava(t *testing.T) (*Fava, string) {
	t.Helper()
	binary := *testFavaBinary
	if binary == "" {
		t.Skip("set TELEBEANS_TEST_FAVA to a Fava executable to run real API integration tests")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "all.beancount")
	current := filepath.Join(dir, "current.beancount")
	for p, s := range map[string]string{root: fixture, current: currentFixture} {
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cmd := exec.Command(binary, "--host", "127.0.0.1", "--port", fmt.Sprint(port), root)
	cmd.Dir = dir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	f := NewFava(fmt.Sprintf("http://127.0.0.1:%d/test", port))
	for n := 0; n < 100; n++ {
		if _, err = f.Ledger(context.Background()); err == nil {
			return f, current
		}
		if strings.Contains(err.Error(), "HTTP 500") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	t.Fatalf("Fava failed to start: %v\n%s", err, output.String())
	return nil, ""
}

type telegramFake struct {
	t        *testing.T
	latest   string
	keyboard Keyboard
	id       int64
	calls    int
}

func (fake *telegramFake) serve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text      string `json:"text"`
		ChatID    int64  `json:"chat_id"`
		MessageID int64  `json:"message_id"`
		Markup    struct {
			Keyboard Keyboard `json:"inline_keyboard"`
		} `json:"reply_markup"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fake.t.Error(err)
	}
	fake.calls++
	switch filepath.Base(r.URL.Path) {
	case "sendMessage":
		fake.id++
		fallthrough
	case "editMessageText":
		fake.latest = body.Text
		fake.keyboard = body.Markup.Keyboard
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": fake.id, "chat": map[string]any{"id": body.ChatID, "type": "private"}}})
}
func (fake *telegramFake) button(t *testing.T, text string) string {
	t.Helper()
	for _, row := range fake.keyboard {
		for _, b := range row {
			if strings.Contains(b.Text, text) {
				return b.Data
			}
		}
	}
	t.Fatalf("no button %s: %+v", text, fake.keyboard)
	return ""
}

func TestTelegramFavaWorkflow(t *testing.T) {
	f, current := disposableFava(t)
	ctx := context.Background()
	state, err := OpenState(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	fake := &telegramFake{t: t}
	server := httptest.NewServer(http.HandlerFunc(fake.serve))
	defer server.Close()
	app := App{Config: testConfig(), Fava: f, Telegram: &Telegram{Base: server.URL, HTTP: server.Client()}, State: state}
	messageID := int64(0)
	send := func(text string) {
		t.Helper()
		messageID++
		err := app.Handle(ctx, Update{ID: messageID, Message: &Message{ID: messageID, From: User{123}, Chat: Chat{123, "private"}, Date: time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC).Unix(), Text: text}})
		if err != nil {
			t.Fatal(err)
		}
	}
	click := func(label string) {
		t.Helper()
		data := fake.button(t, label)
		err := app.Handle(ctx, Update{Callback: &Callback{ID: "callback", From: User{123}, Message: Message{ID: fake.id, Chat: Chat{123, "private"}}, Data: data}})
		if err != nil {
			t.Fatal(err)
		}
	}
	check := func(id, payment, amount, narration string) {
		t.Helper()
		e, err := f.Find(ctx, id)
		if err != nil || e == nil {
			t.Fatalf("missing %s: %v; Telegram: %s", id, err, fake.latest)
		}
		d, err := DraftFromEntry(e.Entry)
		if err != nil || d.Payment != payment || FormatAmount(d.Expenses[0].Minor) != amount || d.Narration != narration {
			t.Fatalf("wrong entry %+v: %v", d, err)
		}
		i, err := state.Get(id)
		if err != nil || i == nil || i.Draft != nil {
			t.Fatalf("saved transaction must only be a ledger reference: %+v %v", i, err)
		}
	}
	send("дома 350 2026-09-30")
	check("m123_1", "Assets:Raif:RSD", "350", "Еда")
	entry, err := f.Find(ctx, "m123_1")
	if err != nil || entry == nil || entry.Entry.Date != "2026-09-30" {
		t.Fatalf("explicit date did not reach Fava: %+v %v", entry, err)
	}
	contents, _ := os.ReadFile(current)
	if !strings.Contains(string(contents), "telebeans_id: \"m123_1\"") {
		t.Fatal("entry did not reach configured current.beancount")
	}
	click("Оплата:")
	click("Cash › RSD")
	check("m123_1", "Assets:Cash:RSD", "350", "Еда")
	click("Изменить")
	click("Сумма")
	send("350,50")
	check("m123_1", "Assets:Cash:RSD", "350.50", "Еда")
	click("Изменить")
	click("Описание")
	send("хлеб и \"молоко\"")
	check("m123_1", "Assets:Cash:RSD", "350.50", "хлеб и \"молоко\"")
	// Re-delivery of the original Telegram message keeps one transaction.
	if err = app.Handle(ctx, Update{Message: &Message{ID: 1, From: User{123}, Chat: Chat{123, "private"}, Date: time.Now().Unix(), Text: "дома 350"}}); err != nil {
		t.Fatal(err)
	}
	check("m123_1", "Assets:Cash:RSD", "350.50", "хлеб и \"молоко\"")
	send("аренда 400 EUR за октябрь")
	if e, err := f.Find(ctx, "m123_4"); err != nil || e != nil {
		t.Fatalf("EUR saved before choosing payment: %v %+v", err, e)
	}
	click("Cash › EUR")
	check("m123_4", "Assets:Cash:EUR", "400", "за октябрь")
	send("блины 500")
	click("Жизнь › Едим › Кафе")
	check("m123_5", "Assets:Raif:RSD", "500", "блины")
	click("Запомнить")
	send("блины 600")
	check("m123_6", "Assets:Raif:RSD", "600", "блины")
	click("Отменить")
	e, err := f.Find(ctx, "m123_6")
	if err != nil || e != nil {
		t.Fatalf("undo failed: %+v %v", e, err)
	}
	before := fake.calls
	if err = app.Handle(ctx, Update{Message: &Message{ID: 100, From: User{999}, Chat: Chat{123, "private"}, Text: "дома 100"}}); err != nil {
		t.Fatal(err)
	}
	if fake.calls != before {
		t.Fatal("replied to unauthorized user")
	}
	app.Config.Reminders = []Reminder{{ID: "rent", Narration: "Счёт", Day: 1, Time: "10:00", Currency: "RSD", Postings: []ReminderPosting{{"Expenses:Дом:Аренда", "1000"}, {"Expenses:Жизнь:Едим:Дома", "200"}}}}
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, app.Config.Location)
	if err = app.Reminders(ctx, now); err != nil {
		t.Fatal(err)
	}
	click("Изменить сумму")
	send("1100 250")
	click("Добавить")
	check("rrent_202610", "Assets:Raif:RSD", "1100", "Счёт")
	e, err = f.Find(ctx, "rrent_202610")
	if err != nil || len(e.Entry.Postings) != 3 || e.Entry.Postings[2].Amount != "-1350 RSD" {
		t.Fatalf("split bill: %+v %v", e, err)
	}
	before = fake.calls
	if err = app.Reminders(ctx, now); err != nil {
		t.Fatal(err)
	}
	if fake.calls != before {
		t.Fatal("repeated paid reminder")
	}
}
