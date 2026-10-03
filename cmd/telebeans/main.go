package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"telebeans/internal/telebeans"
)

func main() {
	log.SetFlags(log.Ltime)
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	if err := telebeans.LoadEnv(".env"); err != nil {
		return err
	}
	configPath := os.Getenv("TELEBEANS_CONFIG")
	if configPath == "" {
		configPath = "config.json"
	}
	flag.StringVar(&configPath, "config", configPath, "configuration JSON file")
	flag.Parse()
	command := "run"
	if flag.NArg() > 0 {
		command = flag.Arg(0)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	tg := telebeans.NewTelegram(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if command == "whoami" {
		if os.Getenv("TELEGRAM_BOT_TOKEN") == "" {
			return fmt.Errorf("set TELEGRAM_BOT_TOKEN in .env first")
		}
		if err := tg.Identity(ctx); err != nil {
			return err
		}
		fmt.Println("Send /start to your new bot in a private chat. No transactions will be written.")
		for ctx.Err() == nil {
			updates, err := tg.Updates(ctx, 0, 20)
			if err != nil {
				return err
			}
			for _, u := range updates {
				if u.Message != nil && u.Message.Chat.Type == "private" {
					fmt.Printf("TELEGRAM_USER_ID=%d\n", u.Message.From.ID)
					return nil
				}
			}
		}
		return nil
	}
	cfg, err := telebeans.LoadConfig(configPath)
	if err != nil {
		return err
	}
	fava := telebeans.NewFava(cfg.FavaURL)
	if command == "check" {
		ledger, err := fava.Ledger(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("Fava: %s\nAccounts: %d\nCurrencies: %v\nDefault insertion file: %s\n", cfg.FavaURL, len(ledger.Accounts), ledger.Currencies, ledger.FavaOptions.DefaultFile)
		if ledger.FavaOptions.DefaultFile == "" {
			fmt.Printf("No Fava default-file configured; fallback is %s\n", ledger.Options.Filename)
		}
		return cfgDraftCheck(cfg, ledger)
	}
	if command != "run" {
		return fmt.Errorf("usage: telebeans [-config config.json] [run|check|whoami]")
	}
	state, err := telebeans.OpenState(cfg.DBPath)
	if err != nil {
		return err
	}
	defer state.Close()
	app := telebeans.App{Config: cfg, Fava: fava, Telegram: tg, State: state}
	return app.Run(ctx)
}
func cfgDraftCheck(c telebeans.Config, l telebeans.Ledger) error {
	for alias, account := range c.Aliases {
		d := telebeans.Draft{Date: time.Now().In(c.Location).Format("2006-01-02"), Currency: c.DefaultCurrency, Payment: c.DefaultPayment, Expenses: []telebeans.Expense{{Account: account.Account, Minor: 100}}}
		if err := l.Validate(d); err != nil {
			return fmt.Errorf("alias %q: %w", alias, err)
		}
	}
	return nil
}
