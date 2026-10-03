package telebeans

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ReminderPosting struct {
	Account string `json:"account"`
	Amount  string `json:"amount"`
}
type Reminder struct {
	ID        string            `json:"id"`
	Narration string            `json:"narration"`
	Day       int               `json:"day"`
	Time      string            `json:"time"`
	Currency  string            `json:"currency"`
	Payment   string            `json:"payment"`
	Postings  []ReminderPosting `json:"postings"`
}

type Config struct {
	Timezone        string            `json:"timezone"`
	DefaultCurrency string            `json:"default_currency"`
	DefaultPayment  string            `json:"default_payment"`
	Aliases         map[string]Alias  `json:"aliases"`
	Payments        map[string]string `json:"payments"`
	Reminders       []Reminder        `json:"reminders"`
	Token           string            `json:"-"`
	UserID          int64             `json:"-"`
	FavaURL         string            `json:"-"`
	DBPath          string            `json:"-"`
	Location        *time.Location    `json:"-"`
}

func LoadEnv(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			return fmt.Errorf("invalid .env assignment")
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, val); err != nil {
				return err
			}
		}
	}
	return s.Err()
}

func LoadConfig(path string) (Config, error) {
	c := Config{Timezone: "Europe/Belgrade", DefaultCurrency: "RSD", DefaultPayment: "Assets:Raif:RSD", Aliases: map[string]Alias{}, Payments: map[string]string{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	c.Location, err = time.LoadLocation(c.Timezone)
	if err != nil {
		return c, err
	}
	c.Token = os.Getenv("TELEGRAM_BOT_TOKEN")
	c.UserID, _ = strconv.ParseInt(os.Getenv("TELEGRAM_USER_ID"), 10, 64)
	c.FavaURL = os.Getenv("FAVA_URL")
	if c.FavaURL == "" {
		c.FavaURL = "https://f.lan.testchamber.one/rs"
	}
	c.DBPath = os.Getenv("TELEBEANS_DB")
	if c.DBPath == "" {
		c.DBPath = "telebeans.db"
	}
	for key, alias := range c.Aliases {
		if key != strings.ToLower(key) {
			delete(c.Aliases, key)
			c.Aliases[strings.ToLower(key)] = alias
		}
	}
	seen := map[string]bool{}
	for _, r := range c.Reminders {
		if !regexp.MustCompile(`^[a-z0-9_-]{1,24}$`).MatchString(r.ID) || seen[r.ID] || r.Day < 1 || r.Day > 31 || len(r.Postings) == 0 {
			return c, fmt.Errorf("invalid reminder %q", r.ID)
		}
		seen[r.ID] = true
		if _, err := time.Parse("15:04", r.Time); err != nil {
			return c, fmt.Errorf("reminder %s: time must be HH:MM", r.ID)
		}
		for _, p := range r.Postings {
			if _, err := ParseAmount(p.Amount); err != nil {
				return c, fmt.Errorf("reminder %s: %w", r.ID, err)
			}
		}
	}
	return c, nil
}
