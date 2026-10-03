package telebeans

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
)

type App struct {
	Config   Config
	Fava     *Fava
	Telegram *Telegram
	State    *State
}

const helpText = `Формат: [дата] категория сумма [валюта] [@оплата] [описание]

дома 350
дома 350 хлеб и молоко
кафе 500. блины
дома 350 @нал
вчера аренда 400 EUR за октябрь
дома 350 хлеб 2026-09-30

Дата YYYY-MM-DD может стоять в любом месте сообщения.

По умолчанию: RSD, Raif. Для EUR выбираем оплату кнопками.
/accounts — категории
/bills — шаблоны счетов
/bill ID — открыть шаблон
/cancel — закончить текущий ввод`

func (a *App) Run(ctx context.Context) error {
	if a.Config.Token == "" || a.Config.UserID <= 0 {
		return fmt.Errorf("set TELEGRAM_BOT_TOKEN and TELEGRAM_USER_ID in .env (use 'telebeans whoami' to find the ID)")
	}
	if err := a.Telegram.Identity(ctx); err != nil {
		return err
	}
	if _, err := a.Fava.Ledger(ctx); err != nil {
		return err
	}
	offset, err := a.State.Offset()
	if err != nil {
		return err
	}
	log.Print("Bot is running; send /start in your private Telegram chat")
	for ctx.Err() == nil {
		if err = a.Reminders(ctx, time.Now()); err != nil {
			log.Printf("Reminders: %v", err)
		}
		updates, err := a.Telegram.Updates(ctx, offset, 20)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Print(err)
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			if err = a.Handle(ctx, u); err != nil {
				log.Printf("Update %d: %v", u.ID, err)
			}
			offset = u.ID + 1
			if err = a.State.SetOffset(offset); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) Handle(ctx context.Context, u Update) error {
	var chat int64
	var err error
	if u.Message != nil && u.Message.From.ID == a.Config.UserID && u.Message.Chat.Type == "private" {
		chat = u.Message.Chat.ID
		err = a.message(ctx, *u.Message)
	} else if u.Callback != nil && u.Callback.From.ID == a.Config.UserID && u.Callback.Message.Chat.Type == "private" {
		chat = u.Callback.Message.Chat.ID
		if err = a.Telegram.Answer(ctx, u.Callback.ID); err != nil {
			return err
		}
		err = a.callback(ctx, *u.Callback)
	} else {
		return nil
	}
	if err != nil {
		_, sendErr := a.Telegram.Send(ctx, chat, "Не получилось: "+err.Error(), nil)
		if sendErr != nil {
			return fmt.Errorf("%v; %v", err, sendErr)
		}
	}
	return err
}

func (a *App) message(ctx context.Context, m Message) error {
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return nil
	}
	if text == "/start" || text == "/help" {
		_, err := a.Telegram.Send(ctx, m.Chat.ID, helpText, nil)
		return err
	}
	if text == "/cancel" {
		if err := a.State.ClearPending(m.Chat.ID); err != nil {
			return err
		}
		_, err := a.Telegram.Send(ctx, m.Chat.ID, "Ввод отменён.", nil)
		return err
	}
	if text == "/accounts" {
		cfg, err := a.withAliases()
		if err != nil {
			return err
		}
		keys := []string{}
		for k := range cfg.Aliases {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var lines []string
		for _, k := range keys {
			lines = append(lines, k+" → "+cfg.Aliases[k].Account)
		}
		_, err = a.Telegram.Send(ctx, m.Chat.ID, strings.Join(lines, "\n"), nil)
		return err
	}
	if text == "/bills" {
		lines := []string{"Ежемесячные шаблоны:"}
		for _, r := range a.Config.Reminders {
			lines = append(lines, fmt.Sprintf("/bill %s — %s (%d, %s)", r.ID, r.Narration, r.Day, r.Time))
		}
		if len(a.Config.Reminders) == 0 {
			lines = append(lines, "Добавьте reminders в config.json и перезапустите бота.")
		}
		_, err := a.Telegram.Send(ctx, m.Chat.ID, strings.Join(lines, "\n"), nil)
		return err
	}
	if strings.HasPrefix(text, "/bill ") {
		id := strings.TrimSpace(strings.TrimPrefix(text, "/bill "))
		for _, r := range a.Config.Reminders {
			if r.ID == id {
				return a.promptBill(ctx, r, time.Now().In(a.Config.Location))
			}
		}
		return fmt.Errorf("неизвестный шаблон; используйте /bills")
	}
	if strings.HasPrefix(text, "/") {
		return fmt.Errorf("неизвестная команда; используйте /help")
	}
	pending, err := a.State.Pending(m.Chat.ID)
	if err != nil {
		return err
	}
	if pending != nil {
		return a.finishInput(ctx, pending, text)
	}
	cfg, err := a.withAliases()
	if err != nil {
		return err
	}
	d, err := ParseInput(text, time.Unix(m.Date, 0), cfg)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("m%d_%d", m.Chat.ID, m.ID)
	i, err := a.State.Get(key)
	if err != nil {
		return err
	}
	if i == nil {
		i = &Interaction{Key: key, ChatID: m.Chat.ID, Draft: &d, Alias: d.UnknownAlias}
	}
	return a.finish(ctx, i)
}

func (a *App) withAliases() (Config, error) {
	cfg := a.Config
	cfg.Aliases = map[string]Alias{}
	for k, v := range a.Config.Aliases {
		cfg.Aliases[k] = v
	}
	learned, err := a.State.Aliases()
	if err != nil {
		return cfg, err
	}
	for k, v := range learned {
		cfg.Aliases[k] = v
	}
	return cfg, nil
}
func (a *App) render(ctx context.Context, i *Interaction, text string, k Keyboard) error {
	if err := a.State.Save(i); err != nil {
		return err
	}
	if i.MessageID == 0 {
		m, err := a.Telegram.Send(ctx, i.ChatID, text, k)
		if err != nil {
			return err
		}
		i.MessageID = m.ID
		return a.State.Save(i)
	}
	return a.Telegram.Edit(ctx, i.ChatID, i.MessageID, text, k)
}
func summary(d Draft) string {
	lines := []string{d.Date + " · " + d.Narration}
	for _, e := range d.Expenses {
		lines = append(lines, FormatAmount(e.Minor)+" "+d.Currency+" → "+e.Account)
	}
	lines = append(lines, "Оплата: "+d.Payment)
	return strings.Join(lines, "\n")
}
func shortAccount(s string) string {
	s = strings.TrimPrefix(s, "Assets:")
	s = strings.TrimPrefix(s, "Expenses:")
	return strings.ReplaceAll(s, ":", " › ")
}
func (a *App) current(ctx context.Context, i *Interaction) (Draft, error) {
	if i.LedgerID == "" {
		if i.Draft == nil {
			return Draft{}, fmt.Errorf("ввод уже отменён")
		}
		return *i.Draft, nil
	}
	e, err := a.Fava.Find(ctx, i.LedgerID)
	if err != nil {
		return Draft{}, err
	}
	if e == nil {
		return Draft{}, fmt.Errorf("транзакция уже удалена")
	}
	return DraftFromEntry(e.Entry)
}
func (a *App) showSaved(ctx context.Context, i *Interaction) error {
	d, err := a.current(ctx, i)
	if err != nil {
		return err
	}
	i.Draft = nil
	i.Mode = ""
	i.Choices = nil
	k := Keyboard{{{Text: "Оплата: " + shortAccount(d.Payment) + " ▾", Data: "pay:" + i.Key}}, {{Text: "Изменить", Data: "edit:" + i.Key}, {Text: "Отменить", Data: "undo:" + i.Key}}}
	if i.Alias != "" {
		k = append(k, []Button{{Text: "Запомнить «" + i.Alias + "»", Data: "remember:" + i.Key}})
	}
	return a.render(ctx, i, "Добавлено в Fava\n"+summary(d), k)
}
func (a *App) finish(ctx context.Context, i *Interaction) error {
	if i.LedgerID != "" {
		return a.showSaved(ctx, i)
	}
	if i.Draft == nil {
		return fmt.Errorf("ввод отменён")
	}
	d := *i.Draft
	l, err := a.Fava.Ledger(ctx)
	if err != nil {
		return err
	}
	if d.Expenses[0].Account == "" {
		return a.choose(ctx, i, "category", l.Choices("Expenses:", "", d.Date), 0)
	}
	if d.Payment == "" {
		return a.choose(ctx, i, "payment", l.Choices("Assets:", d.Currency, d.Date), 0)
	}
	if err = a.State.Save(i); err != nil {
		return err
	}
	e, err := a.Fava.Add(ctx, d, i.Key)
	if err != nil {
		return a.render(ctx, i, "Не удалось сохранить: "+err.Error(), Keyboard{{{Text: "Повторить", Data: "add:" + i.Key}, {Text: "Отменить", Data: "cancel:" + i.Key}}})
	}
	i.LedgerID = e.Entry.ID()
	i.Draft = nil
	i.Mode = ""
	if i.Reminder != "" {
		if err = a.State.SetReminder(i.Reminder, "added", 0); err != nil {
			return err
		}
	}
	return a.showSaved(ctx, i)
}

func (a *App) choose(ctx context.Context, i *Interaction, mode string, choices []string, page int) error {
	if len(choices) == 0 {
		return fmt.Errorf("нет подходящих счетов в Fava")
	}
	i.Mode = mode
	i.Choices = choices
	const size = 8
	if page < 0 || page*size >= len(choices) {
		page = 0
	}
	k := Keyboard{}
	for n := page * size; n < len(choices) && n < (page+1)*size; n++ {
		k = append(k, []Button{{Text: shortAccount(choices[n]), Data: fmt.Sprintf("pick:%s:%d", i.Key, n)}})
	}
	nav := []Button{}
	if page > 0 {
		nav = append(nav, Button{"←", fmt.Sprintf("page:%s:%d", i.Key, page-1)})
	}
	if (page+1)*size < len(choices) {
		nav = append(nav, Button{"→", fmt.Sprintf("page:%s:%d", i.Key, page+1)})
	}
	if len(nav) > 0 {
		k = append(k, nav)
	}
	back := "cancel:" + i.Key
	if i.LedgerID != "" {
		back = "back:" + i.Key
	}
	k = append(k, []Button{{Text: "Назад / отмена", Data: back}})
	d, err := a.current(ctx, i)
	if err != nil {
		return err
	}
	label := "Выберите оплату"
	if mode == "category" {
		label = "Выберите категорию для «" + i.Alias + "»"
	}
	return a.render(ctx, i, label+"\n"+summary(d), k)
}

func (a *App) callback(ctx context.Context, c Callback) error {
	p := strings.Split(c.Data, ":")
	if len(p) < 2 {
		return fmt.Errorf("неизвестная кнопка")
	}
	i, err := a.State.Get(p[1])
	if err != nil {
		return err
	}
	if i == nil || i.ChatID != c.Message.Chat.ID || i.MessageID != c.Message.ID {
		return fmt.Errorf("это действие больше недоступно")
	}
	switch p[0] {
	case "add":
		return a.finish(ctx, i)
	case "cancel":
		i.Draft = nil
		i.Mode = ""
		return a.render(ctx, i, "Ввод отменён.", nil)
	case "back":
		return a.showSaved(ctx, i)
	case "undo":
		if i.LedgerID == "" {
			return fmt.Errorf("транзакция ещё не сохранена")
		}
		if err = a.Fava.Delete(ctx, i.LedgerID); err != nil {
			return err
		}
		i.Draft = nil
		i.Mode = ""
		return a.render(ctx, i, "Транзакция удалена из Fava.", nil)
	case "pay":
		d, err := a.current(ctx, i)
		if err != nil {
			return err
		}
		l, err := a.Fava.Ledger(ctx)
		if err != nil {
			return err
		}
		return a.choose(ctx, i, "payment", l.Choices("Assets:", d.Currency, d.Date), 0)
	case "page":
		if len(p) != 3 {
			return fmt.Errorf("неверная кнопка")
		}
		page, err := strconv.Atoi(p[2])
		if err != nil {
			return err
		}
		return a.choose(ctx, i, i.Mode, i.Choices, page)
	case "pick":
		if len(p) != 3 {
			return fmt.Errorf("неверная кнопка")
		}
		n, err := strconv.Atoi(p[2])
		if err != nil || n < 0 || n >= len(i.Choices) {
			return fmt.Errorf("выбор больше недоступен")
		}
		d, err := a.current(ctx, i)
		if err != nil {
			return err
		}
		if i.Mode == "category" {
			d.Expenses[0].Account = i.Choices[n]
			d.UnknownAlias = ""
		} else if i.Mode == "payment" {
			d.Payment = i.Choices[n]
		} else {
			return fmt.Errorf("выбор больше недоступен")
		}
		i.Mode = ""
		i.Choices = nil
		if i.LedgerID == "" {
			i.Draft = &d
			return a.finish(ctx, i)
		}
		if _, err = a.Fava.Edit(ctx, i.LedgerID, d); err != nil {
			return err
		}
		return a.showSaved(ctx, i)
	case "remember":
		if i.Alias == "" {
			return a.showSaved(ctx, i)
		}
		d, err := a.current(ctx, i)
		if err != nil {
			return err
		}
		if err = a.State.Remember(strings.ToLower(i.Alias), Alias{d.Expenses[0].Account, d.Narration}); err != nil {
			return err
		}
		i.Alias = ""
		return a.showSaved(ctx, i)
	case "edit":
		return a.render(ctx, i, "Что изменить?", Keyboard{{{Text: "Сумма", Data: "amount:" + i.Key}, {Text: "Описание", Data: "narration:" + i.Key}, {Text: "Дата", Data: "date:" + i.Key}}, {{Text: "Назад", Data: "back:" + i.Key}}})
	case "amount", "narration", "date":
		if err = a.State.ClearPending(i.ChatID); err != nil {
			return err
		}
		d, err := a.current(ctx, i)
		if err != nil {
			return err
		}
		// Only unsaved drafts are persisted. Edits read the transaction again on reply.
		i.Mode = p[0]
		label := "Введите описание"
		if p[0] == "date" {
			label = "Введите дату YYYY-MM-DD"
		}
		if p[0] == "amount" {
			label = "Введите сумму"
			if len(d.Expenses) > 1 {
				label = "Введите суммы проводок через пробел, в указанном порядке"
			}
		}
		back := "cancel:" + i.Key
		if i.LedgerID != "" {
			back = "back:" + i.Key
		}
		return a.render(ctx, i, label+"\n"+summary(d), Keyboard{{{Text: "Отмена", Data: back}}})
	case "skip":
		if i.Reminder == "" {
			return fmt.Errorf("это не напоминание")
		}
		if err = a.State.SetReminder(i.Reminder, "skipped", 0); err != nil {
			return err
		}
		i.Mode = ""
		i.Draft = nil
		return a.render(ctx, i, "Пропущено в этом месяце.", nil)
	case "snooze":
		if i.Reminder == "" {
			return fmt.Errorf("это не напоминание")
		}
		if err = a.State.SetReminder(i.Reminder, "snoozed", time.Now().Add(24*time.Hour).Unix()); err != nil {
			return err
		}
		i.Mode = ""
		return a.render(ctx, i, "Напомню через 24 часа.", nil)
	}
	return fmt.Errorf("неизвестная кнопка")
}

func (a *App) finishInput(ctx context.Context, i *Interaction, text string) error {
	d, err := a.current(ctx, i)
	if err != nil {
		return err
	}
	switch i.Mode {
	case "amount":
		values := strings.Fields(text)
		if len(values) != len(d.Expenses) {
			return fmt.Errorf("нужно ввести %d сумм через пробел; /cancel для отмены", len(d.Expenses))
		}
		for n, s := range values {
			amount, err := ParseAmount(s)
			if err != nil {
				return err
			}
			d.Expenses[n].Minor = amount
		}
	case "narration":
		d.Narration = text
	case "date":
		if _, err := time.Parse("2006-01-02", text); err != nil {
			return fmt.Errorf("дата должна быть YYYY-MM-DD")
		}
		d.Date = text
	default:
		return fmt.Errorf("используйте кнопки или /cancel")
	}
	i.Mode = ""
	if i.LedgerID != "" {
		if _, err = a.Fava.Edit(ctx, i.LedgerID, d); err != nil {
			return err
		}
		return a.showSaved(ctx, i)
	}
	i.Draft = &d
	return a.showBill(ctx, i)
}

func (a *App) billDraft(r Reminder, now time.Time) (Draft, error) {
	d := Draft{Date: now.Format("2006-01-02"), Narration: r.Narration, Currency: r.Currency, Payment: r.Payment}
	if d.Currency == "" {
		d.Currency = a.Config.DefaultCurrency
	}
	if d.Payment == "" && d.Currency == a.Config.DefaultCurrency {
		d.Payment = a.Config.DefaultPayment
	}
	for _, p := range r.Postings {
		n, err := ParseAmount(p.Amount)
		if err != nil {
			return d, err
		}
		d.Expenses = append(d.Expenses, Expense{p.Account, n})
	}
	return d, nil
}
func (a *App) promptBill(ctx context.Context, r Reminder, now time.Time) error {
	period := r.ID + "/" + now.Format("2006-01")
	d, err := a.billDraft(r, now)
	if err != nil {
		return err
	}
	key := "r" + r.ID + "_" + now.Format("200601")
	i, err := a.State.Get(key)
	if err != nil {
		return err
	}
	if i == nil {
		i = &Interaction{Key: key, ChatID: a.Config.UserID, Reminder: period, Draft: &d}
	}
	if i.LedgerID != "" {
		return a.showSaved(ctx, i)
	}
	if i.Draft == nil {
		i.Draft = &d
	}
	if err = a.showBill(ctx, i); err != nil {
		return err
	}
	return a.State.SetReminder(period, "prompted", 0)
}
func (a *App) showBill(ctx context.Context, i *Interaction) error {
	if i.Draft == nil {
		return fmt.Errorf("нет шаблона")
	}
	return a.render(ctx, i, "Записать оплату?\n"+summary(*i.Draft), Keyboard{{{Text: "Добавить", Data: "add:" + i.Key}, {Text: "Изменить сумму", Data: "amount:" + i.Key}}, {{Text: "Напомнить позже", Data: "snooze:" + i.Key}, {Text: "Пропустить", Data: "skip:" + i.Key}}})
}

func (a *App) Reminders(ctx context.Context, now time.Time) error {
	now = now.In(a.Config.Location)
	for _, r := range a.Config.Reminders {
		lastDay := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, a.Config.Location).Day()
		day := r.Day
		if day > lastDay {
			day = lastDay
		}
		clock, _ := time.Parse("15:04", r.Time)
		due := time.Date(now.Year(), now.Month(), day, clock.Hour(), clock.Minute(), 0, 0, a.Config.Location)
		status, snooze, err := a.State.Reminder(r.ID + "/" + now.Format("2006-01"))
		if err != nil {
			return err
		}
		if status == "snoozed" {
			if now.Unix() < snooze {
				continue
			}
		} else if status != "" || now.Before(due) {
			continue
		}
		if err = a.promptBill(ctx, r, now); err != nil {
			return err
		}
	}
	return nil
}
