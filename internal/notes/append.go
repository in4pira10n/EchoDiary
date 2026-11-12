package notes

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"html"
	"strings"
	"time"
)

var ErrHandoffScope = errors.New("日记不在接力文件夹中")

var ErrAppendConflict = errors.New("requestId 已用于其他内容，请为新的内容生成新的 UUID")

// Append serializes read/modify/write with editor saves. Request receipts travel
// with the library's existing durable provenance journal and survive index rebuilds.
func (s *Store) Append(id, text, format, source, requestID string) (saved Note, duplicate bool, err error) {
	return s.appendContent(id, text, format, source, requestID, false)
}

// AppendHandoff checks folder membership under the same lock as the content write.
func (s *Store) AppendHandoff(id, text, format, source, requestID string) (Note, bool, error) {
	return s.appendContent(id, text, format, source, requestID, true)
}

func (s *Store) appendContent(id, text, format, source, requestID string, handoffOnly bool) (saved Note, duplicate bool, err error) {
	err = s.mutate(func() error {
		items, err := s.listIDs([]string{id})
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return sql.ErrNoRows
		}
		if handoffOnly {
			folders, err := s.readFolders()
			if err != nil {
				return err
			}
			allowed := false
			for _, folder := range folders {
				if folder.ID == HandoffFolderID {
					for _, member := range folder.NoteIDs {
						if member == id {
							allowed = true
						}
					}
				}
			}
			if !allowed {
				return ErrHandoffScope
			}
		}
		saved = items[0]
		digest := sha256.Sum256([]byte(id + "\x00" + format + "\x00" + text))
		receiptSource := "handoff:" + source + ":" + requestID
		receiptKey := hex.EncodeToString(digest[:])
		if requestID != "" {
			receipts, err := s.importedKeys(context.Background(), receiptSource)
			if err != nil {
				return err
			}
			if len(receipts) > 0 {
				if !receipts[receiptKey] {
					return ErrAppendConflict
				}
				duplicate = true
				return nil
			}
		}
		escaped := func(value string) string {
			return "<p>" + strings.ReplaceAll(html.EscapeString(value), "\n", "<br>") + "</p>"
		}
		if saved.BodyFormat == "markdown" {
			chunk := text
			if format != "markdown" {
				// Plain clipboard text stays literal even inside a Markdown diary.
				chunk = html.EscapeString(text)
				chunk = strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "#", "\\#", "`", "\\`", "~", "\\~", ".", "\\.", "-", "\\-", "+", "\\+", "!", "\\!", "|", "\\|").Replace(chunk)
			}
			saved.BodyMarkdown = strings.TrimRight(saved.BodyMarkdown, "\n") + "\n\n" + chunk
		} else {
			original := saved.BodyHTML
			if original == "" && saved.Body != "" {
				original = escaped(saved.Body)
			}
			chunk := escaped(text)
			if format == "markdown" {
				chunk = normalizeContent(Note{BodyFormat: "markdown", BodyMarkdown: text}).BodyHTML
			}
			saved.BodyHTML = original + chunk
		}
		saved = normalizeContent(saved)
		if len(saved.BodyHTML)+len(saved.BodyMarkdown) > 8<<20 {
			return errors.New("日记内容超过 8 MB")
		}
		saved.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.Exec(`UPDATE notes SET body=?,body_html=?,body_markdown=?,body_format=? WHERE id=?`, saved.Body, saved.BodyHTML, saved.BodyMarkdown, saved.BodyFormat, id); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO note_timestamps(id,updated_at) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET updated_at=excluded.updated_at`, id, saved.UpdatedAt); err != nil {
			return err
		}
		if requestID != "" {
			if _, err = tx.Exec(`INSERT INTO note_imports(source,entry_key,note_id) VALUES(?,?,?)`, receiptSource, receiptKey, id); err != nil {
				return err
			}
		}
		return tx.Commit()
	}, id)
	return
}
