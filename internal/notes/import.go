package notes

import (
	"context"
	"encoding/json"
	"fmt"
)

type ImportEntry struct {
	Key  string
	Note Note
}

func (s *Store) importedKeys(ctx context.Context, source string) (map[string]bool, error) {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS note_imports (
		source TEXT NOT NULL, entry_key TEXT NOT NULL, note_id TEXT NOT NULL,
		PRIMARY KEY (source, entry_key)
	)`); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT entry_key FROM note_imports WHERE source=?`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make(map[string]bool)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys[key] = true
	}
	return keys, rows.Err()
}

// Import commits notes and their provenance together, without overwriting edits
// when an export is imported again (even after the library moves).
func (s *Store) importNotes(ctx context.Context, source string, entries []ImportEntry) (int, error) {
	if _, err := s.importedKeys(ctx, source); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	count := 0
	for _, entry := range entries {
		if entry.Key == "" || entry.Note.ID == "" || entry.Note.IsEmpty() {
			return 0, fmt.Errorf("invalid import entry")
		}
		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO note_imports(source,entry_key,note_id) VALUES(?,?,?)`, source, entry.Key, entry.Note.ID)
		if err != nil {
			return 0, err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if inserted == 0 {
			continue
		}
		n := normalizeContent(entry.Note)
		if n.Images == nil {
			n.Images = []string{}
		}
		images, err := json.Marshal(n.Images)
		if err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO notes(id,title,body,images_json,source,favorite,created_at,body_html,body_markdown,body_format) VALUES(?,?,?,?,?,?,?,?,?,?)`, n.ID, n.Title, n.Body, string(images), boolInt(n.Source), boolInt(n.Favorite), n.CreatedAt, n.BodyHTML, n.BodyMarkdown, n.BodyFormat); err != nil {
			return 0, err
		}
		count++
	}
	if count > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE app_meta SET value='1' WHERE key='initialized'`); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}
