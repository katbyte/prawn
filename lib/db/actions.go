package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Action kinds and statuses. Unlike koi there is no propose/approve pipeline —
// the checks act directly, so the ledger records what happened, keeps the
// rejected guard, and gives reopen something to flip.
const (
	ActionClose = "close"
	ActionLabel = "label"

	StatusApplied  = "applied"
	StatusFailed   = "failed"
	StatusRejected = "rejected" // a human said no in an interactive apply
	StatusReopened = "reopened"
)

// Action is one audited mutation: what was done to a PR, on what evidence, and
// by whose decision.
type Action struct {
	ID         int64
	PRNumber   int
	Action     string
	Reason     string
	Template   string
	Evidence   map[string]string
	Confidence float64
	Source     string // the pass that proposed it
	Status     string
	DecidedBy  string
	AppliedAt  string
	Error      string
}

// RecordAction appends one action row.
func (d *DB) RecordAction(a *Action) error {
	evidence, err := json.Marshal(a.Evidence)
	if err != nil {
		return fmt.Errorf("marshalling evidence for #%d: %w", a.PRNumber, err)
	}
	if _, err := d.Exec(`
		INSERT INTO actions (pr_number, action, reason, template, evidence, confidence, source, status, decided_by, applied_at, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.PRNumber, a.Action, a.Reason, a.Template, string(evidence), a.Confidence,
		a.Source, a.Status, a.DecidedBy, toDBTime(Now()), a.Error); err != nil {
		return fmt.Errorf("recording action for #%d: %w", a.PRNumber, err)
	}
	return nil
}

// LastAction returns the most recent action row for a PR, or nil.
func (d *DB) LastAction(number int) (*Action, error) {
	row := d.QueryRow(`
		SELECT id, pr_number, action, reason, template, evidence, confidence, source, status, decided_by, applied_at, error
		FROM actions WHERE pr_number = ? ORDER BY id DESC LIMIT 1`, number)

	var a Action
	var evidence string
	err := row.Scan(&a.ID, &a.PRNumber, &a.Action, &a.Reason, &a.Template, &evidence,
		&a.Confidence, &a.Source, &a.Status, &a.DecidedBy, &a.AppliedAt, &a.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading action for #%d: %w", number, err)
	}
	if err := json.Unmarshal([]byte(evidence), &a.Evidence); err != nil {
		a.Evidence = map[string]string{}
	}
	return &a, nil
}

// SetActionStatus updates one action row's status (reopen's flip).
func (d *DB) SetActionStatus(id int64, status string) error {
	if _, err := d.Exec("UPDATE actions SET status = ? WHERE id = ?", status, id); err != nil {
		return fmt.Errorf("updating action %d: %w", id, err)
	}
	return nil
}

// Rejected reports whether a human already rejected an action on this PR in an
// interactive apply. The checks re-derive candidates from evidence on every
// run, so without this guard a skipped candidate would be re-proposed and
// closed by the next auto apply.
func (d *DB) Rejected(number int) (bool, error) {
	a, err := d.LastAction(number)
	if err != nil {
		return false, err
	}
	return a != nil && a.Status == StatusRejected, nil
}

// Verdict is one cached AI judgement row.
type Verdict struct {
	PRNumber   int
	Pass       string
	PromptHash string
	Model      string
	Verdict    string
	Confidence float64
}

// GetVerdict returns the cached verdict for a PR+pass, or nil.
func (d *DB) GetVerdict(number int, pass string) (*Verdict, error) {
	row := d.QueryRow("SELECT pr_number, pass, prompt_hash, model, verdict, confidence FROM ai_verdicts WHERE pr_number = ? AND pass = ?", number, pass)
	var v Verdict
	err := row.Scan(&v.PRNumber, &v.Pass, &v.PromptHash, &v.Model, &v.Verdict, &v.Confidence)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading verdict for #%d/%s: %w", number, pass, err)
	}
	return &v, nil
}

// SaveVerdict upserts a verdict row.
func (d *DB) SaveVerdict(v *Verdict) error {
	if _, err := d.Exec(`
		INSERT INTO ai_verdicts (pr_number, pass, prompt_hash, model, verdict, confidence, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pr_number, pass) DO UPDATE SET
			prompt_hash = excluded.prompt_hash, model = excluded.model, verdict = excluded.verdict,
			confidence = excluded.confidence, created_at = excluded.created_at`,
		v.PRNumber, v.Pass, v.PromptHash, v.Model, v.Verdict, v.Confidence, toDBTime(Now())); err != nil {
		return fmt.Errorf("saving verdict for #%d/%s: %w", v.PRNumber, v.Pass, err)
	}
	return nil
}
