package telebeans

import (
	"database/sql"
	"encoding/json"
	"errors"

	_ "modernc.org/sqlite"
)

// Saved transactions have only a ledger reference here. Their contents always come from Fava.
type Interaction struct {
	Key       string   `json:"key"`
	ChatID    int64    `json:"chat_id"`
	MessageID int64    `json:"message_id"`
	LedgerID  string   `json:"ledger_id"`
	Draft     *Draft   `json:"draft,omitempty"`
	Mode      string   `json:"mode,omitempty"`
	Choices   []string `json:"choices,omitempty"`
	Alias     string   `json:"alias,omitempty"`
	Reminder  string   `json:"reminder,omitempty"`
}
type State struct{ db *sql.DB }

func OpenState(path string) (*State, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS interactions (key TEXT PRIMARY KEY, chat INTEGER NOT NULL, awaiting INTEGER NOT NULL, body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS aliases (alias TEXT PRIMARY KEY, account TEXT NOT NULL, narration TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS reminders (period TEXT PRIMARY KEY, status TEXT NOT NULL, due INTEGER NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &State{db}, nil
}
func (s *State) Close() error { return s.db.Close() }
func (s *State) Save(i *Interaction) error {
	b, err := json.Marshal(i)
	if err != nil {
		return err
	}
	awaiting := 0
	if i.Mode == "amount" || i.Mode == "narration" || i.Mode == "date" {
		awaiting = 1
	}
	_, err = s.db.Exec(`INSERT INTO interactions VALUES (?, ?, ?, ?) ON CONFLICT(key) DO UPDATE SET chat=excluded.chat, awaiting=excluded.awaiting, body=excluded.body`, i.Key, i.ChatID, awaiting, string(b))
	return err
}
func decodeInteraction(row *sql.Row) (*Interaction, error) {
	var b string
	if err := row.Scan(&b); errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var i Interaction
	err := json.Unmarshal([]byte(b), &i)
	return &i, err
}
func (s *State) Get(key string) (*Interaction, error) {
	return decodeInteraction(s.db.QueryRow(`SELECT body FROM interactions WHERE key=?`, key))
}
func (s *State) Pending(chat int64) (*Interaction, error) {
	return decodeInteraction(s.db.QueryRow(`SELECT body FROM interactions WHERE chat=? AND awaiting=1 ORDER BY rowid DESC LIMIT 1`, chat))
}
func (s *State) ClearPending(chat int64) error {
	rows, err := s.db.Query(`SELECT body FROM interactions WHERE chat=? AND awaiting=1`, chat)
	if err != nil {
		return err
	}
	var pending []*Interaction
	for rows.Next() {
		var b string
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return err
		}
		var i Interaction
		if err = json.Unmarshal([]byte(b), &i); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, &i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, i := range pending {
		i.Mode = ""
		if i.LedgerID != "" {
			i.Draft = nil
		}
		if err = s.Save(i); err != nil {
			return err
		}
	}
	return nil
}
func (s *State) Aliases() (map[string]Alias, error) {
	rows, err := s.db.Query(`SELECT alias,account,narration FROM aliases`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]Alias{}
	for rows.Next() {
		var key string
		var a Alias
		if err = rows.Scan(&key, &a.Account, &a.Narration); err != nil {
			return nil, err
		}
		result[key] = a
	}
	return result, rows.Err()
}
func (s *State) Remember(alias string, a Alias) error {
	_, err := s.db.Exec(`INSERT INTO aliases VALUES (?,?,?) ON CONFLICT(alias) DO UPDATE SET account=excluded.account,narration=excluded.narration`, alias, a.Account, a.Narration)
	return err
}
func (s *State) Offset() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key='offset'`).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return n, err
}
func (s *State) SetOffset(n int64) error {
	_, err := s.db.Exec(`INSERT INTO settings VALUES ('offset',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, n)
	return err
}
func (s *State) Reminder(period string) (string, int64, error) {
	var status string
	var due int64
	err := s.db.QueryRow(`SELECT status,due FROM reminders WHERE period=?`, period).Scan(&status, &due)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return status, due, err
}
func (s *State) SetReminder(period, status string, due int64) error {
	_, err := s.db.Exec(`INSERT INTO reminders VALUES (?,?,?) ON CONFLICT(period) DO UPDATE SET status=excluded.status,due=excluded.due`, period, status, due)
	return err
}
