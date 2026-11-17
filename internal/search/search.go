// Package search indexes the conversation text of every agmux session so it
// can be searched across sessions (experimental feature "session_search").
//
// Stream JSONL files under ~/.agmux/streams are read incrementally by byte
// offset; only user/assistant text is indexed (tool input/output is skipped).
// Matching uses LIKE so that Japanese text (no word boundaries) works.
package search

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/myuon/agmux/internal/redact"
	"github.com/myuon/agmux/internal/session"
)

// FeatureName is the experimental flag that gates this package.
const FeatureName = "session_search"

const maxTextBytes = 20000

// Indexer keeps the search index in sync with the stream files.
type Indexer struct {
	db         *sql.DB
	streamsDir string
	logger     *slog.Logger
	enabled    func() bool

	syncMu   sync.Mutex
	kick     chan struct{}
	indexing atomic.Bool
}

// New creates an Indexer and ensures its tables exist.
func New(db *sql.DB, streamsDir string, enabled func() bool, logger *slog.Logger) (*Indexer, error) {
	if err := ensureSchema(db); err != nil {
		return nil, err
	}
	return &Indexer{
		db:         db,
		streamsDir: streamsDir,
		logger:     logger.With("component", "search"),
		enabled:    enabled,
		kick:       make(chan struct{}, 1),
	}, nil
}

func ensureSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS search_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			text TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_search_messages_session ON search_messages(session_id);
		CREATE TABLE IF NOT EXISTS search_offsets (
			session_id TEXT PRIMARY KEY,
			offset INTEGER NOT NULL
		);
	`)
	return err
}

// Run syncs the index periodically while the feature is enabled, and
// immediately whenever Kick is called. It returns when ctx is done.
func (ix *Indexer) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if ix.enabled() {
			if err := ix.Sync(ctx); err != nil && !errors.Is(err, context.Canceled) {
				ix.logger.Warn("search index sync failed", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-ix.kick:
		}
	}
}

// Kick requests an immediate sync (non-blocking).
func (ix *Indexer) Kick() {
	select {
	case ix.kick <- struct{}{}:
	default:
	}
}

// Indexing reports whether a sync is currently running.
func (ix *Indexer) Indexing() bool { return ix.indexing.Load() }

// Sync indexes any new lines appended to stream files since the last sync,
// and drops entries of sessions that no longer exist.
func (ix *Indexer) Sync(ctx context.Context) error {
	ix.syncMu.Lock()
	defer ix.syncMu.Unlock()
	ix.indexing.Store(true)
	defer ix.indexing.Store(false)

	if _, err := ix.db.Exec(`DELETE FROM search_messages WHERE session_id NOT IN (SELECT id FROM sessions)`); err != nil {
		return err
	}
	if _, err := ix.db.Exec(`DELETE FROM search_offsets WHERE session_id NOT IN (SELECT id FROM sessions)`); err != nil {
		return err
	}

	rows, err := ix.db.Query(`SELECT s.id, s.provider, COALESCE(o.offset, 0) FROM sessions s LEFT JOIN search_offsets o ON o.session_id = s.id`)
	if err != nil {
		return err
	}
	type target struct {
		id, provider string
		offset       int64
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.provider, &t.offset); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, t := range targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ix.syncSession(t.id, t.provider, t.offset); err != nil {
			ix.logger.Warn("search index: session sync failed", "sessionId", t.id, "error", err)
		}
	}
	return nil
}

func (ix *Indexer) syncSession(id, provider string, offset int64) error {
	path := filepath.Join(ix.streamsDir, id+".jsonl")
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if st.Size() < offset {
		// File was truncated/replaced: rebuild this session from scratch.
		if _, err := ix.db.Exec(`DELETE FROM search_messages WHERE session_id = ?`, id); err != nil {
			return err
		}
		offset = 0
	}
	if st.Size() == offset {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}

	p := session.GetProvider(session.ProviderName(provider), "", "")
	r := bufio.NewReaderSize(f, 1<<20)

	tx, err := ix.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ins, err := tx.Prepare(`INSERT INTO search_messages (session_id, role, text) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()

	pos := offset
	for {
		line, err := r.ReadBytes('\n')
		if len(line) == 0 || line[len(line)-1] != '\n' {
			// Incomplete trailing line: leave it for the next sync.
			break
		}
		pos += int64(len(line))
		for _, nl := range p.NormalizeStreamLine(line[:len(line)-1]) {
			for _, m := range ExtractMessages(nl) {
				if _, err := ins.Exec(id, m.Role, m.Text); err != nil {
					return err
				}
			}
		}
		if err != nil {
			break
		}
	}
	if _, err := tx.Exec(`INSERT INTO search_offsets (session_id, offset) VALUES (?, ?)
		ON CONFLICT(session_id) DO UPDATE SET offset = excluded.offset`, id, pos); err != nil {
		return err
	}
	return tx.Commit()
}

// Message is a piece of indexable conversation text.
type Message struct {
	Role string
	Text string
}

// ExtractMessages pulls user/assistant text out of a Claude-compatible
// stream-json line. Tool use and tool results are ignored.
func ExtractMessages(line []byte) []Message {
	var ev struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &ev) != nil {
		return nil
	}
	if ev.Type != "user" && ev.Type != "assistant" {
		return nil
	}
	var texts []string
	var s string
	if json.Unmarshal(ev.Message.Content, &s) == nil {
		texts = append(texts, s)
	} else {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(ev.Message.Content, &blocks) != nil {
			return nil
		}
		for _, b := range blocks {
			if b.Type == "text" {
				texts = append(texts, b.Text)
			}
		}
	}
	var out []Message
	for _, t := range texts {
		t = strings.TrimSpace(t)
		if t == "" || isInjected(t) {
			continue
		}
		if len(t) > maxTextBytes {
			t = truncateUTF8(t, maxTextBytes)
		}
		out = append(out, Message{Role: ev.Type, Text: redact.Redact(t)})
	}
	return out
}

// injectedPrefixes mark text that the harness injects into the conversation
// (skill bodies, system reminders, task notifications) rather than something a
// person or the agent actually said.
var injectedPrefixes = []string{
	"<system-reminder>",
	"<task-notification>",
	"<command-name>",
	"<command-message>",
	"<local-command-",
	"Base directory for this skill:",
	"Caveat: The messages below were generated by the user while running local commands",
}

func isInjected(t string) bool {
	for _, p := range injectedPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func truncateUTF8(s string, n int) string {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Query describes a search request.
type Query struct {
	Text           string
	ProjectPath    string
	ExcludeSession string
	Limit          int
}

// Result is a single hit.
type Result struct {
	SessionID   string    `json:"sessionId"`
	SessionName string    `json:"sessionName"`
	ProjectPath string    `json:"projectPath"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Role        string    `json:"role"`
	Snippet     string    `json:"snippet"`
}

// Search returns messages containing every whitespace-separated term of
// q.Text (case-insensitive for ASCII), newest first.
func (ix *Indexer) Search(q Query) ([]Result, error) {
	terms := strings.Fields(q.Text)
	if len(terms) == 0 {
		return []Result{}, nil
	}
	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	where := []string{}
	args := []interface{}{}
	for _, t := range terms {
		where = append(where, `m.text LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(t)+"%")
	}
	if q.ProjectPath != "" {
		where = append(where, `s.project_path = ?`)
		args = append(args, q.ProjectPath)
	}
	if q.ExcludeSession != "" {
		where = append(where, `m.session_id != ?`)
		args = append(args, q.ExcludeSession)
	}
	args = append(args, limit)
	rows, err := ix.db.Query(fmt.Sprintf(`
		SELECT m.session_id, s.name, s.project_path, s.updated_at, m.role, m.text
		FROM search_messages m JOIN sessions s ON s.id = m.session_id
		WHERE %s ORDER BY s.updated_at DESC, m.id DESC LIMIT ?`, strings.Join(where, " AND ")), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Result{}
	for rows.Next() {
		var r Result
		var text string
		var updated sql.NullTime
		if err := rows.Scan(&r.SessionID, &r.SessionName, &r.ProjectPath, &updated, &r.Role, &text); err != nil {
			return nil, err
		}
		if updated.Valid {
			r.UpdatedAt = updated.Time
		}
		r.Snippet = Snippet(text, terms[0], 80)
		out = append(out, r)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// Snippet returns up to `radius` runes of context on each side of the first
// case-insensitive occurrence of term in text.
func Snippet(text, term string, radius int) string {
	runes := []rune(text)
	lower := []rune(strings.ToLower(text))
	tr := []rune(strings.ToLower(term))
	idx := -1
	if len(lower) == len(runes) {
		for i := 0; i+len(tr) <= len(lower); i++ {
			if string(lower[i:i+len(tr)]) == string(tr) {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		idx = 0
	}
	start := idx - radius
	if start < 0 {
		start = 0
	}
	end := idx + len(tr) + radius
	if end > len(runes) {
		end = len(runes)
	}
	s := strings.Join(strings.Fields(string(runes[start:end])), " ")
	if start > 0 {
		s = "…" + s
	}
	if end < len(runes) {
		s += "…"
	}
	return s
}
