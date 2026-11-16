package chathistory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

type Store struct {
	db         *storage.DB
	instanceID string
}

func NewStore(db *storage.DB, instanceID string) *Store {
	return &Store{db: db, instanceID: instanceID}
}

const recordColumns = `id, platform, message_type, target_id, message_id, sender_id, sender_name, role, content, reply_to, occurred_at, delivery_status`

func (s *Store) Save(ctx context.Context, r Record) error {
	return s.SaveTx(ctx, s.db.SQL, r)
}

// SaveTx supports atomic archive imports as well as idempotent live writes.
func (s *Store) SaveTx(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, r Record) error {
	r.Scope = r.Scope.Canonical()
	if err := r.Validate(); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, s.db.Bind(`INSERT INTO chat_history
		(instance_id, `+recordColumns+`, search_text) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (instance_id, id) DO NOTHING`), s.instanceID, r.ID, r.Platform, r.MessageType,
		r.TargetID, r.MessageID, r.SenderID, r.SenderName, r.Role, r.Content, r.ReplyTo,
		r.OccurredAt.UnixMilli(), r.DeliveryStatus, strings.ToLower(r.Content))
	return err
}

func (s *Store) Search(ctx context.Context, scope Scope, query Query) ([]Record, error) {
	scope = scope.Canonical()
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if query.Match != "" && query.Match != "all" && query.Match != "any" {
		return nil, fmt.Errorf("keyword match must be all or any")
	}
	keywords := make([]string, 0, len(query.Keywords))
	seen := map[string]bool{}
	for _, term := range query.Keywords {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		if len([]rune(term)) > 128 {
			return nil, fmt.Errorf("keyword is too long")
		}
		if !seen[term] {
			keywords = append(keywords, term)
			seen[term] = true
		}
	}
	if len(keywords) == 0 || len(keywords) > 6 {
		return nil, fmt.Errorf("search requires 1 to 6 keywords")
	}
	if !query.After.IsZero() && !query.Before.IsZero() && !query.After.Before(query.Before) {
		return nil, fmt.Errorf("after must precede before")
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > MaxSearchResults {
		limit = MaxSearchResults
	}
	where := `instance_id = ? AND platform = ? AND message_type = ? AND target_id = ?`
	args := []any{s.instanceID, scope.Platform, scope.MessageType, scope.TargetID}
	clauses := make([]string, 0, len(keywords))
	for _, term := range keywords {
		// Treat SQL wildcard characters as literal parts of the keyword.
		term = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(term)
		clauses = append(clauses, `search_text LIKE ? ESCAPE '!'`)
		args = append(args, "%"+term+"%")
	}
	join := " AND "
	if query.Match == "any" {
		join = " OR "
	}
	where += " AND (" + strings.Join(clauses, join) + ")"
	if !query.After.IsZero() {
		where += " AND occurred_at >= ?"
		args = append(args, query.After.UnixMilli())
	}
	if !query.Before.IsZero() {
		where += " AND occurred_at < ?"
		args = append(args, query.Before.UnixMilli())
	}
	args = append(args, limit)
	rows, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT `+recordColumns+` FROM chat_history WHERE `+where+` ORDER BY occurred_at DESC, id DESC LIMIT ?`), args...)
	if err != nil {
		return nil, err
	}
	return readRecords(rows)
}

// Export retains complete originals. Retrieval snippets never replace them.
func (s *Store) Export(ctx context.Context) ([]Record, error) {
	rows, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT `+recordColumns+` FROM chat_history WHERE instance_id = ? ORDER BY occurred_at, id`), s.instanceID)
	if err != nil {
		return nil, err
	}
	return readRecords(rows)
}

func readRecords(rows *sql.Rows) ([]Record, error) {
	defer rows.Close()
	result := []Record{}
	for rows.Next() {
		var r Record
		var occurred int64
		if err := rows.Scan(&r.ID, &r.Platform, &r.MessageType, &r.TargetID, &r.MessageID, &r.SenderID, &r.SenderName, &r.Role, &r.Content, &r.ReplyTo, &occurred, &r.DeliveryStatus); err != nil {
			return nil, err
		}
		r.OccurredAt = time.UnixMilli(occurred).UTC()
		result = append(result, r)
	}
	return result, rows.Err()
}

type Hit struct {
	Record
	Truncated bool `json:"truncated,omitempty"`
}

// Snippets limits the tool result while preserving exact original text slices.
func Snippets(records []Record, keywords []string) []Hit {
	result := make([]Hit, 0, len(records))
	remaining := 8000
	for _, record := range records {
		if remaining <= 0 {
			break
		}
		text := []rune(record.Content)
		limit := min(1200, remaining)
		hit := Hit{Record: record, Truncated: len(text) > limit}
		if hit.Truncated {
			lower := []rune(strings.ToLower(record.Content))
			start := 0
			for _, keyword := range keywords {
				keyword = strings.ToLower(strings.TrimSpace(keyword))
				if keyword == "" {
					continue
				}
				if index := strings.Index(string(lower), keyword); index >= 0 {
					start = max(0, len([]rune(string(lower)[:index]))-100)
					break
				}
			}
			start = min(start, len(text)-limit)
			hit.Content = string(text[start : start+limit])
		}
		remaining -= len([]rune(hit.Content))
		result = append(result, hit)
	}
	return result
}
