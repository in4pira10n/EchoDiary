package notes

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Note struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Body         string   `json:"body"`
	BodyHTML     string   `json:"bodyHtml,omitempty"`
	BodyMarkdown string   `json:"bodyMarkdown,omitempty"`
	BodyFormat   string   `json:"bodyFormat,omitempty"`
	Images       []string `json:"images"`
	Source       bool     `json:"source,omitempty"`
	Favorite     bool     `json:"favorite,omitempty"`
	CreatedAt    string   `json:"createdAt,omitempty"`
	UpdatedAt    string   `json:"updatedAt,omitempty"`
}

func (n Note) IsEmpty() bool {
	if strings.TrimSpace(n.Title) != "" || strings.TrimSpace(n.Body) != "" || strings.Contains(n.BodyHTML, "<input") || (n.BodyFormat == "markdown" && strings.TrimSpace(n.BodyMarkdown) != "") {
		return false
	}
	for _, image := range n.Images {
		if strings.TrimSpace(image) != "" {
			return false
		}
	}
	return true
}

type Store struct {
	lock                *os.File
	db                  *sql.DB
	mu                  sync.RWMutex
	dataDir, entriesDir string
	media               map[string]string
	entryFolders        map[string]string
	foldersPath         string
	fault               error
}
type TrashNote struct {
	Note
	DeletedAt string `json:"deletedAt"`
}

func Open(dataDir string) (*Store, error) {
	return open(dataDir, true)
}

// OpenForLibrary opens a library during an in-process switch without doing the
// startup cleanup pass over every note row.
func OpenForLibrary(dataDir string) (*Store, error) {
	return open(dataDir, false)
}

func open(dataDir string, cleanupEmpty bool) (*Store, error) {
	var lock *os.File
	success := false
	defer func() {
		if !success && lock != nil {
			_ = lock.Close()
		}
	}()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	if filepath.Base(filepath.Clean(dataDir)) == ".echodiary" {
		var err error
		lock, err = lockLibrary(dataDir)
		if err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "notes.sqlite"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS notes (
		id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '', body TEXT NOT NULL DEFAULT '',
		images_json TEXT NOT NULL DEFAULT '[]', source INTEGER NOT NULL DEFAULT 0,
		favorite INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS app_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err = db.Exec(`INSERT OR IGNORE INTO app_meta (key, value) VALUES ('initialized', '0')`); err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &Store{db: db, lock: lock, foldersPath: filepath.Join(dataDir, "folders.json")}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS note_timestamps (id TEXT PRIMARY KEY, updated_at TEXT NOT NULL)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS trash_notes (
		id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '', body TEXT NOT NULL DEFAULT '',
		images_json TEXT NOT NULL DEFAULT '[]', source INTEGER NOT NULL DEFAULT 0,
		favorite INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT '', deleted_at TEXT NOT NULL
	)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Additive migration preserves existing notes and trash on older libraries.
	for _, table := range []string{"notes", "trash_notes"} {
		for _, column := range []string{"body_html", "body_markdown", "body_format"} {
			rows, err := db.Query("PRAGMA table_info(" + table + ")")
			if err != nil {
				db.Close()
				return nil, err
			}
			found := false
			for rows.Next() {
				var cid, notNull, pk int
				var name, kind string
				var defaultValue any
				if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
					rows.Close()
					db.Close()
					return nil, err
				}
				if name == column {
					found = true
				}
			}
			rowsErr := rows.Err()
			rows.Close()
			if rowsErr != nil {
				db.Close()
				return nil, rowsErr
			}
			if !found {
				if _, err := db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " TEXT NOT NULL DEFAULT ''"); err != nil {
					db.Close()
					return nil, err
				}
			}
		}
	}
	if cleanupEmpty && !fileStoreExists(dataDir) {
		legacyNotes, err := store.list()
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		for _, note := range legacyNotes {
			if note.IsEmpty() {
				if err := store.deleteEmpty(note.ID); err != nil {
					_ = db.Close()
					return nil, err
				}
			}
		}
		var count int
		if err = db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&count); err != nil {
			_ = db.Close()
			return nil, err
		}
		if count > 0 {
			if _, err = db.Exec(`UPDATE app_meta SET value='1' WHERE key='initialized'`); err != nil {
				_ = db.Close()
				return nil, err
			}
		}
	} else {
		var initialized string
		if err := db.QueryRow(`SELECT value FROM app_meta WHERE key='initialized'`).Scan(&initialized); err != nil {
			_ = db.Close()
			return nil, err
		}
		if initialized != "1" {
			var hasNotes int
			if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM notes LIMIT 1)`).Scan(&hasNotes); err != nil {
				_ = db.Close()
				return nil, err
			}
			if hasNotes != 0 {
				if _, err := db.Exec(`UPDATE app_meta SET value='1' WHERE key='initialized'`); err != nil {
					_ = db.Close()
					return nil, err
				}
			}
		}
	}

	if filepath.Base(filepath.Clean(dataDir)) == ".echodiary" {
		store.dataDir = dataDir
		store.entriesDir = filepath.Join(filepath.Dir(dataDir), "entries")
		if err := store.openFiles(); err != nil {
			db.Close()
			return nil, err
		}
	}
	success = true
	return store, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.Close()
	if s.lock != nil {
		closeErr := s.lock.Close()
		s.lock = nil
		if err == nil {
			err = closeErr
		}
	}
	return err
}

func (s *Store) bootstrapPending() (bool, error) {
	var value string
	if err := s.db.QueryRow(`SELECT value FROM app_meta WHERE key='initialized'`).Scan(&value); err != nil {
		return false, err
	}
	return value != "1", nil
}

// Initialize imports the first browser's local notes exactly once. If a note
// was already written, preserve it and only close the bootstrap window.
func (s *Store) initialize(seed []Note) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var initialized string
	if err := tx.QueryRow(`SELECT value FROM app_meta WHERE key='initialized'`).Scan(&initialized); err != nil {
		return err
	}
	if initialized == "1" {
		return tx.Commit()
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		for _, note := range seed {
			note = normalizeContent(note)
			if note.IsEmpty() {
				continue
			}
			if note.ID == "" {
				return fmt.Errorf("note id is required")
			}
			if note.Images == nil {
				note.Images = []string{}
			}
			if note.CreatedAt == "" {
				note.CreatedAt = "创建于：" + time.Now().Format("2006年1月2日 15:04")
			}
			images, err := json.Marshal(note.Images)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO notes (id,title,body,images_json,source,favorite,created_at,body_html,body_markdown,body_format) VALUES (?,?,?,?,?,?,?,?,?,?)`, note.ID, note.Title, note.Body, string(images), boolInt(note.Source), boolInt(note.Favorite), note.CreatedAt, note.BodyHTML, note.BodyMarkdown, note.BodyFormat); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`UPDATE app_meta SET value='1' WHERE key='initialized'`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) list() ([]Note, error) { return s.listIDs(nil) }
func (s *Store) listIDs(ids []string) ([]Note, error) {
	filter, args := idFilter(ids, "n.id")
	rows, err := s.db.Query(`SELECT n.id, title, body, images_json, source, favorite, created_at, COALESCE(t.updated_at, created_at), body_html, body_markdown, body_format FROM notes n LEFT JOIN note_timestamps t ON t.id=n.id `+filter+` ORDER BY n.rowid DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Note, 0)
	for rows.Next() {
		var n Note
		var images string
		var source, favorite int
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &images, &source, &favorite, &n.CreatedAt, &n.UpdatedAt, &n.BodyHTML, &n.BodyMarkdown, &n.BodyFormat); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(images), &n.Images)
		if n.Images == nil {
			n.Images = []string{}
		}
		n.Source, n.Favorite = source != 0, favorite != 0
		result = append(result, n)
	}
	return result, rows.Err()
}

func (s *Store) counts() (notesCount, imagesCount, favoritesCount int, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(json_array_length(images_json)), 0), COALESCE(SUM(favorite), 0) FROM notes`).Scan(&notesCount, &imagesCount, &favoritesCount)
	return
}

func (s *Store) upsert(n Note) (Note, error) {
	n = normalizeContent(n)
	if n.ID == "" {
		return n, fmt.Errorf("note id is required")
	}
	if n.IsEmpty() {
		return n, s.deleteEmpty(n.ID)
	}
	if n.Images == nil {
		n.Images = []string{}
	}
	if n.CreatedAt == "" {
		n.CreatedAt = "创建于：" + time.Now().Format("2006年1月2日 15:04")
	}
	images, err := json.Marshal(n.Images)
	if err != nil {
		return n, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return n, err
	}
	defer tx.Rollback()
	var changed bool
	err = tx.QueryRow(`SELECT NOT(title=? AND body=? AND images_json=? AND body_html=? AND body_markdown=? AND body_format=?) FROM notes WHERE id=?`, n.Title, n.Body, string(images), n.BodyHTML, n.BodyMarkdown, n.BodyFormat, n.ID).Scan(&changed)
	if err != nil && err != sql.ErrNoRows {
		return n, err
	}
	if !changed && err == nil {
		if err = tx.QueryRow(`SELECT COALESCE((SELECT updated_at FROM note_timestamps WHERE id=?), created_at) FROM notes WHERE id=?`, n.ID, n.ID).Scan(&n.UpdatedAt); err != nil {
			return n, err
		}
	}
	if changed || err == sql.ErrNoRows {
		n.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if _, err = tx.Exec(`INSERT INTO note_timestamps(id,updated_at) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET updated_at=excluded.updated_at`, n.ID, n.UpdatedAt); err != nil {
			return n, err
		}
	}
	_, err = tx.Exec(`INSERT INTO notes (id,title,body,images_json,source,favorite,created_at,body_html,body_markdown,body_format) VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET title=excluded.title, body=excluded.body, images_json=excluded.images_json, source=excluded.source, favorite=excluded.favorite, created_at=excluded.created_at, body_html=excluded.body_html, body_markdown=excluded.body_markdown, body_format=excluded.body_format`, n.ID, n.Title, n.Body, string(images), boolInt(n.Source), boolInt(n.Favorite), n.CreatedAt, n.BodyHTML, n.BodyMarkdown, n.BodyFormat)
	if err == nil {
		_, err = tx.Exec(`UPDATE app_meta SET value='1' WHERE key='initialized'`)
	}
	if err == nil {
		err = tx.Commit()
	}
	return n, err
}

func (s *Store) Delete(id string) error {
	return s.MoveToTrash(id)
}

func (s *Store) deleteEmpty(id string) error {
	_, err := s.db.Exec(`DELETE FROM notes WHERE id = ?`, id)
	return err
}

func (s *Store) moveToTrash(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n Note
	var images string
	var source, favorite int
	if err := tx.QueryRow(`SELECT id,title,body,images_json,source,favorite,created_at,body_html,body_markdown,body_format FROM notes WHERE id = ?`, id).
		Scan(&n.ID, &n.Title, &n.Body, &images, &source, &favorite, &n.CreatedAt, &n.BodyHTML, &n.BodyMarkdown, &n.BodyFormat); err != nil {
		return err
	}
	n.Images = []string{}
	_ = json.Unmarshal([]byte(images), &n.Images)
	_, err = tx.Exec(`INSERT OR REPLACE INTO trash_notes (id,title,body,images_json,source,favorite,created_at,deleted_at,body_html,body_markdown,body_format) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.Title, n.Body, images, source, favorite, n.CreatedAt, time.Now().UTC().Format(time.RFC3339Nano), n.BodyHTML, n.BodyMarkdown, n.BodyFormat)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM notes WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) listTrash() ([]TrashNote, error) { return s.listTrashIDs(nil) }
func (s *Store) listTrashIDs(ids []string) ([]TrashNote, error) {
	filter, args := idFilter(ids, "id")
	rows, err := s.db.Query(`SELECT id,title,body,images_json,source,favorite,created_at,deleted_at,body_html,body_markdown,body_format FROM trash_notes `+filter+` ORDER BY deleted_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TrashNote, 0)
	for rows.Next() {
		var item TrashNote
		var images string
		var source, favorite int
		if err := rows.Scan(&item.ID, &item.Title, &item.Body, &images, &source, &favorite, &item.CreatedAt, &item.DeletedAt, &item.BodyHTML, &item.BodyMarkdown, &item.BodyFormat); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(images), &item.Images)
		if item.Images == nil {
			item.Images = []string{}
		}
		item.Source, item.Favorite = source != 0, favorite != 0
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) restore(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n Note
	var images string
	var source, favorite int
	if err := tx.QueryRow(`SELECT id,title,body,images_json,source,favorite,created_at,body_html,body_markdown,body_format FROM trash_notes WHERE id = ?`, id).
		Scan(&n.ID, &n.Title, &n.Body, &images, &source, &favorite, &n.CreatedAt, &n.BodyHTML, &n.BodyMarkdown, &n.BodyFormat); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT OR REPLACE INTO notes (id,title,body,images_json,source,favorite,created_at,body_html,body_markdown,body_format) VALUES (?,?,?,?,?,?,?,?,?,?)`, n.ID, n.Title, n.Body, images, source, favorite, n.CreatedAt, n.BodyHTML, n.BodyMarkdown, n.BodyFormat); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM trash_notes WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) permanentlyDelete(id string, trashRoot string) (Note, error) {
	if err := validID(id); err != nil {
		return Note{}, err
	}
	items, err := s.listTrash()
	if err != nil {
		return Note{}, err
	}
	var found *TrashNote
	for i := range items {
		if items[i].ID == id {
			found = &items[i]
			break
		}
	}
	if found == nil {
		return Note{}, sql.ErrNoRows
	}
	if err := os.MkdirAll(filepath.Join(trashRoot, id, "images"), 0o755); err != nil {
		return Note{}, err
	}
	data, err := json.MarshalIndent(found, "", "  ")
	if err != nil {
		return Note{}, err
	}
	if err := os.WriteFile(filepath.Join(trashRoot, id, "note.json"), data, 0o644); err != nil {
		return Note{}, err
	}
	for _, url := range found.Images {
		name := filepath.Base(url)
		if strings.HasPrefix(url, "/media/") {
			from := filepath.Join(filepath.Dir(trashRoot), "images", name)
			to := filepath.Join(trashRoot, id, "images", name)
			if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
				return Note{}, err
			}
		}
	}
	if _, err := s.db.Exec(`DELETE FROM trash_notes WHERE id = ?`, id); err != nil {
		return Note{}, err
	}
	return found.Note, nil
}

func (s *Store) usesImage(url string) (bool, error) {
	rows, err := s.db.Query(`SELECT images_json FROM notes`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return false, err
		}
		var images []string
		if err := json.Unmarshal([]byte(encoded), &images); err != nil {
			continue
		}
		for _, image := range images {
			if image == url {
				return true, nil
			}
		}
	}
	return false, rows.Err()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
