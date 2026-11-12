package notes

// The file store owns content. SQLite is a disposable query index. A durable
// pending commit bridges multiple JSON writes; startup always recovers it before
// rebuilding the index. Older standalone database callers retain their API.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type importRecord struct {
	Source string `json:"source"`
	Key    string `json:"key"`
	NoteID string `json:"noteId"`
}
type fileMeta struct {
	Version     int            `json:"version"`
	Initialized bool           `json:"initialized"`
	Order       []string       `json:"order"`
	Imports     []importRecord `json:"imports"`
}
type fileLocation struct {
	ID    string `json:"id"`
	Trash bool   `json:"trash,omitempty"`
}
type fileSnapshot struct {
	Deletes []fileLocation `json:"deletes,omitempty"`
	Notes   []Note         `json:"notes"`
	Trash   []TrashNote    `json:"trash"`
	Meta    fileMeta       `json:"meta"`
}

func validID(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\\:`) || strings.TrimSpace(id) != id || strings.HasSuffix(id, ".") {
		return fmt.Errorf("invalid note id")
	}
	for _, r := range id {
		if r < 32 || strings.ContainsRune(`<>"|?*`, r) {
			return fmt.Errorf("invalid note id")
		}
	}
	// Windows device names are reserved even when followed by an extension.
	base := strings.ToUpper(strings.Split(id, ".")[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
		return fmt.Errorf("invalid note id")
	}
	return nil
}
func fileStoreExists(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, "storage.json"))
	return err == nil
}
func atomicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicBytes(path, data)
}
func atomicBytes(path string, data []byte) error {
	if old, e := os.ReadFile(path); e == nil && bytes.Equal(old, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0644); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}
func (s *Store) trashDir() string { return filepath.Join(s.dataDir, "recycle-bin") }

// Directory UUIDs preserve UUID note IDs. Older/custom IDs (including Kehua
// fingerprints) receive a deterministic UUID without changing their public ID.
func entryUUID(id string) uuid.UUID {
	if value, err := uuid.Parse(id); err == nil {
		return value
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("echodiary:entry:"+id))
}

// EntryDirectoryName uses creation time in Unix milliseconds, never edit time.
// Legacy Chinese dates carry no zone; interpret them consistently as UTC+8 on
// every host. Unknown historical dates use 0 rather than inventing a date.
func EntryDirectoryName(n Note) string {
	stamp := int64(0)
	value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(n.CreatedAt), "创建于："))
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		stamp = parsed.UnixMilli()
	} else {
		if strings.Contains(value, "年") && strings.Contains(value, "月") && strings.Contains(value, "日") {
			value = strings.Join(strings.Fields(value), "")
			value = strings.TrimSpace(strings.Replace(value, "日", "日 ", 1))
		}
		zone := time.FixedZone("UTC+8", 8*60*60)
		for _, layout := range []string{"2006年1月2日 15:04:05", "2006年1月2日 15:04", "2006年1月2日", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02", "2006/1/2 15:04:05", "2006/1/2 15:04", "2006/1/2"} {
			if parsed, err := time.ParseInLocation(layout, value, zone); err == nil {
				stamp = parsed.UnixMilli()
				break
			}
		}
	}
	return strconv.FormatInt(stamp, 10) + "_" + entryUUID(n.ID).String()
}

func validEntryFolder(folder, id string) bool {
	stamp, suffix, ok := strings.Cut(folder, "_")
	_, err := strconv.ParseInt(stamp, 10, 64)
	return ok && err == nil && suffix == entryUUID(id).String()
}

func (s *Store) noteDir(root string, n Note) string {
	if root == s.entriesDir {
		if folder := s.entryFolders[n.ID]; folder != "" {
			return filepath.Join(root, folder)
		}
		return filepath.Join(root, EntryDirectoryName(n))
	}
	return filepath.Join(root, n.ID)
}

// EntryDirectory resolves the on-disk folder without coupling callers to IDs.
func (s *Store) EntryDirectory(id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.fault != nil {
		return "", s.fault
	}
	if folder := s.entryFolders[id]; folder != "" {
		return filepath.Join(s.entriesDir, folder), nil
	}
	return "", os.ErrNotExist
}

func (s *Store) migrateEntryFolders(items []Note) error {
	for _, n := range items {
		folder := s.entryFolders[n.ID]
		if folder != n.ID { // Already named: preserve the original creation prefix.
			continue
		}
		target := EntryDirectoryName(n)
		if _, err := os.Lstat(filepath.Join(s.entriesDir, target)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("entry directory migration target already exists or is inaccessible: %s", target)
		}
		if err := os.Rename(filepath.Join(s.entriesDir, folder), filepath.Join(s.entriesDir, target)); err != nil {
			return err
		}
		s.entryFolders[n.ID] = target
	}
	return nil
}
func (s *Store) snapshot(ids ...string) (fileSnapshot, error) {
	var state fileSnapshot
	var err error
	state.Notes, err = s.listIDs(ids)
	if err != nil {
		return state, err
	}
	state.Trash, err = s.listTrashIDs(ids)
	if err != nil {
		return state, err
	}
	pending, err := s.bootstrapPending()
	if err != nil {
		return state, err
	}
	// Portable folders must also be unique on case-insensitive filesystems.
	names, err := s.db.Query(`SELECT id FROM notes UNION ALL SELECT id FROM trash_notes`)
	if err != nil {
		return state, err
	}
	seen := map[string]bool{}
	for names.Next() {
		var id string
		if err = names.Scan(&id); err != nil {
			names.Close()
			return state, err
		}
		if err = validID(id); err != nil {
			names.Close()
			return state, err
		}
		key := strings.ToLower(id)
		if seen[key] {
			names.Close()
			return state, fmt.Errorf("duplicate portable note id: %s", id)
		}
		seen[key] = true
	}
	err = names.Err()
	names.Close()
	if err != nil {
		return state, err
	}

	state.Meta = fileMeta{Version: 1, Initialized: !pending, Order: []string{}, Imports: []importRecord{}}
	order, err := s.db.Query(`SELECT id FROM notes ORDER BY rowid DESC`)
	if err != nil {
		return state, err
	}
	for order.Next() {
		var id string
		if err = order.Scan(&id); err != nil {
			order.Close()
			return state, err
		}
		state.Meta.Order = append(state.Meta.Order, id)
	}
	err = order.Err()
	order.Close()
	if err != nil {
		return state, err
	}
	for i := range state.Trash {
		var stamp string
		err := s.db.QueryRow(`SELECT updated_at FROM note_timestamps WHERE id=?`, state.Trash[i].ID).Scan(&stamp)
		if err != nil && err != sql.ErrNoRows {
			return state, err
		}
		state.Trash[i].UpdatedAt = stamp
	}
	if _, err = s.importedKeys(context.Background(), ""); err != nil {
		return state, err
	}
	rows, err := s.db.Query(`SELECT source,entry_key,note_id FROM note_imports ORDER BY source,entry_key`)
	if err != nil {
		return state, err
	}
	defer rows.Close()
	for rows.Next() {
		var r importRecord
		if err = rows.Scan(&r.Source, &r.Key, &r.NoteID); err != nil {
			return state, err
		}
		state.Meta.Imports = append(state.Meta.Imports, r)
	}
	return state, rows.Err()
}
func (s *Store) validate(state fileSnapshot) error {
	if state.Meta.Version != 1 {
		return fmt.Errorf("unsupported file storage version")
	}
	seen := map[string]bool{}
	for _, n := range append(append([]Note{}, state.Notes...), trashNotes(state.Trash)...) {
		if err := validID(n.ID); err != nil {
			return err
		}
		key := strings.ToLower(n.ID)
		if seen[key] {
			return fmt.Errorf("duplicate note id: %s", n.ID)
		}
		seen[key] = true
	}
	return nil
}
func trashNotes(items []TrashNote) []Note {
	out := make([]Note, 0, len(items))
	for _, n := range items {
		out = append(out, n.Note)
	}
	return out
}
func (s *Store) openFiles() error {
	if err := s.refreshMedia(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.entriesDir, 0755); err != nil {
		return err
	}
	journal := filepath.Join(s.dataDir, "pending-commit.json")
	var state fileSnapshot
	if err := readJSON(journal, &state); err == nil {
		if err = s.applyFiles(state); err != nil {
			return err
		}
		if err = os.Remove(journal); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !fileStoreExists(s.dataDir) {
		var err error
		state, err = s.snapshot()
		if err != nil {
			return err
		}
		dirs, err := os.ReadDir(s.entriesDir)
		if err != nil {
			return err
		}
		for _, d := range dirs {
			if d.IsDir() {
				if _, err := os.Stat(filepath.Join(s.entriesDir, d.Name(), "note.json")); err == nil {
					return fmt.Errorf("entries already contains notes but storage metadata is missing; refusing to overwrite")
				}
			}
		}
		if err = s.commitFiles(state); err != nil {
			return err
		}
	}
	var err error
	state, err = s.readFiles()
	if err != nil {
		return err
	}
	if err = s.migrateEntryFolders(state.Notes); err != nil {
		return err
	}
	if err = s.rebuild(state); err != nil {
		return err
	}
	// Reject old binaries after migration instead of allowing them to write only
	// the disposable database. Preserve the library identity and other fields.
	path := filepath.Join(s.dataDir, "library.json")
	var manifest map[string]any
	if err := readJSON(path, &manifest); err == nil {
		if manifest["version"] == float64(1) {
			manifest["version"] = 2
			if err := atomicJSON(path, manifest); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.refreshMedia()
}
func (s *Store) readFiles() (fileSnapshot, error) {
	var state fileSnapshot
	if err := readJSON(filepath.Join(s.dataDir, "storage.json"), &state.Meta); err != nil {
		return state, err
	}
	for _, root := range []string{s.entriesDir, s.trashDir()} {
		dirs, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return state, err
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			var item TrashNote
			err = readJSON(filepath.Join(root, d.Name(), "note.json"), &item)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return state, err
			}
			if item.ID != d.Name() && !(root == s.entriesDir && validEntryFolder(d.Name(), item.ID)) {
				return state, fmt.Errorf("note id differs from directory: %s", d.Name())
			}
			if root == s.entriesDir {
				state.Notes = append(state.Notes, item.Note)
			} else {
				state.Trash = append(state.Trash, item)
			}
		}
	}
	return state, s.validate(state)
}
func (s *Store) rebuild(state fileSnapshot) error {
	if _, err := s.importedKeys(context.Background(), ""); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM notes; DELETE FROM trash_notes; DELETE FROM note_timestamps; DELETE FROM note_imports;`); err != nil {
		return err
	}
	byID := map[string]Note{}
	for _, n := range state.Notes {
		byID[n.ID] = n
	}
	ordered := []Note{}
	for _, id := range state.Meta.Order {
		if n, ok := byID[id]; ok {
			ordered = append(ordered, n)
			delete(byID, id)
		}
	}
	for _, n := range state.Notes {
		if _, ok := byID[n.ID]; ok {
			ordered = append(ordered, n)
		}
	}
	insert := func(n Note, deleted string) error {
		if n.Images == nil {
			n.Images = []string{}
		}
		images, _ := json.Marshal(n.Images)
		args := []any{n.ID, n.Title, n.Body, string(images), boolInt(n.Source), boolInt(n.Favorite), n.CreatedAt, n.BodyHTML, n.BodyMarkdown, n.BodyFormat}
		query := `INSERT INTO notes(id,title,body,images_json,source,favorite,created_at,body_html,body_markdown,body_format) VALUES(?,?,?,?,?,?,?,?,?,?)`
		if deleted != "" {
			query = `INSERT INTO trash_notes(id,title,body,images_json,source,favorite,created_at,body_html,body_markdown,body_format,deleted_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`
			args = append(args, deleted)
		}
		if _, err := tx.Exec(query, args...); err != nil {
			return err
		}
		if n.UpdatedAt != "" {
			_, err := tx.Exec(`INSERT INTO note_timestamps(id,updated_at) VALUES(?,?)`, n.ID, n.UpdatedAt)
			return err
		}
		return nil
	}
	for i := len(ordered) - 1; i >= 0; i-- {
		if err = insert(ordered[i], ""); err != nil {
			return err
		}
	}
	for _, n := range state.Trash {
		if n.DeletedAt == "" {
			return fmt.Errorf("trash note missing deletion timestamp")
		}
		if err = insert(n.Note, n.DeletedAt); err != nil {
			return err
		}
	}
	for _, r := range state.Meta.Imports {
		if _, err = tx.Exec(`INSERT INTO note_imports(source,entry_key,note_id) VALUES(?,?,?)`, r.Source, r.Key, r.NoteID); err != nil {
			return err
		}
	}
	value := "0"
	if state.Meta.Initialized {
		value = "1"
	}
	if _, err = tx.Exec(`UPDATE app_meta SET value=? WHERE key='initialized'`, value); err != nil {
		return err
	}
	return tx.Commit()
}

// prepareImages copies instead of moving, so a failed commit cannot strand the
// old document. Shared pictures get an independent copy in each owning entry.
func (s *Store) prepareImages(root string, n Note) error {
	dir := filepath.Join(s.noteDir(root, n), "images")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for _, url := range n.Images {
		if !strings.HasPrefix(url, "/media/") {
			continue
		}
		name := strings.TrimPrefix(url, "/media/")
		if filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
			return fmt.Errorf("invalid image path")
		}
		target := filepath.Join(dir, name)
		if _, err := os.Stat(target); err == nil {
			continue
		}
		source := s.media[name]
		if source == "" {
			source = filepath.Join(s.dataDir, "images", name)
		}
		data, err := os.ReadFile(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err = atomicBytes(target, data); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) applyFiles(state fileSnapshot) error {
	if err := s.validate(state); err != nil {
		return err
	}
	for _, n := range state.Notes {
		if err := s.prepareImages(s.entriesDir, n); err != nil {
			return err
		}
		dir := s.noteDir(s.entriesDir, n)
		if err := atomicJSON(filepath.Join(dir, "note.json"), n); err != nil {
			return err
		}
		s.entryFolders[n.ID] = filepath.Base(dir)
	}
	for _, n := range state.Trash {
		if err := s.prepareImages(s.trashDir(), n.Note); err != nil {
			return err
		}
		if err := atomicJSON(filepath.Join(s.trashDir(), n.ID, "note.json"), n); err != nil {
			return err
		}
	}
	for _, deleted := range state.Deletes {
		if err := validID(deleted.ID); err != nil {
			return err
		}
		root := s.entriesDir
		if deleted.Trash {
			root = s.trashDir()
		}
		folder := deleted.ID
		if !deleted.Trash && s.entryFolders[deleted.ID] != "" {
			folder = s.entryFolders[deleted.ID]
		}
		if err := os.RemoveAll(filepath.Join(root, folder)); err != nil {
			return err
		}
		if !deleted.Trash {
			delete(s.entryFolders, deleted.ID)
		}
	}

	return atomicJSON(filepath.Join(s.dataDir, "storage.json"), state.Meta)
}
func (s *Store) commitFiles(state fileSnapshot) error {
	if err := s.validate(state); err != nil {
		return err
	}
	// Ensure attachments exist at the destination before committing metadata.
	for _, n := range state.Notes {
		if err := s.prepareImages(s.entriesDir, n); err != nil {
			return err
		}
	}
	for _, n := range state.Trash {
		if err := s.prepareImages(s.trashDir(), n.Note); err != nil {
			return err
		}
	}
	path := filepath.Join(s.dataDir, "pending-commit.json")
	if err := atomicJSON(path, state); err != nil {
		return err
	}
	if err := s.applyFiles(state); err != nil {
		return err
	}
	return os.Remove(path)
}
func (s *Store) refreshMedia() error {
	s.media = map[string]string{}
	loadFolders := s.entryFolders == nil
	if loadFolders {
		s.entryFolders = map[string]string{}
	}
	for _, root := range []string{s.entriesDir, s.trashDir()} {
		dirs, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			if root == s.entriesDir && loadFolders {
				var n Note
				if err := readJSON(filepath.Join(root, d.Name(), "note.json"), &n); err == nil {
					if previous := s.entryFolders[n.ID]; previous != "" {
						return fmt.Errorf("duplicate entry folders for note: %s", n.ID)
					}
					s.entryFolders[n.ID] = d.Name()
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			files, err := os.ReadDir(filepath.Join(root, d.Name(), "images"))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			for _, f := range files {
				if f.Type().IsRegular() {
					s.media[f.Name()] = filepath.Join(root, d.Name(), "images", f.Name())
				}
			}
		}
	}
	return nil
}
func (s *Store) ImagePath(name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.fault != nil {
		return "", s.fault
	}
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return "", os.ErrNotExist
	}
	if p := s.media[name]; p != "" {
		return p, nil
	}
	path := filepath.Join(s.dataDir, "images", name)
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}

// mutate isolates readers while the candidate index is updated, then commits
// the file journal. An uncommitted candidate is discarded on error or restart.
func (s *Store) mutate(fn func() error, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entriesDir == "" {
		return fn()
	}
	// Retry recovery after an interrupted filesystem operation before any new write.
	if _, err := os.Stat(filepath.Join(s.dataDir, "pending-commit.json")); err == nil {
		if err = s.openFiles(); err != nil {
			s.fault = err
			return err
		}
		s.fault = nil
	} else if s.fault != nil {
		if err = s.openFiles(); err != nil {
			return err
		}
		s.fault = nil
	}
	before, err := s.snapshot(ids...)
	if err != nil {
		return err
	}
	if err := fn(); err != nil {
		return err
	}
	state, err := s.snapshot(ids...)
	if err == nil {
		err = s.commitFiles(fileDelta(before, state))
	}
	if err != nil {
		// A durable journal means the commit must finish; do not roll it back.
		if _, journalErr := os.Stat(filepath.Join(s.dataDir, "pending-commit.json")); journalErr == nil {
			if recoveryErr := s.openFiles(); recoveryErr != nil {
				s.fault = recoveryErr
				return errors.Join(err, recoveryErr)
			}
		} else if old, readErr := s.readFiles(); readErr == nil {
			if recoveryErr := s.rebuild(old); recoveryErr != nil {
				s.fault = recoveryErr
				return errors.Join(err, recoveryErr)
			}
		} else {
			s.fault = readErr
			return errors.Join(err, readErr)
		}
		return err
	}
	return s.refreshMedia()
}
func (s *Store) List() ([]Note, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.fault != nil {
		return nil, s.fault
	}
	return s.list()
}
func (s *Store) ListTrash() ([]TrashNote, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.fault != nil {
		return nil, s.fault
	}
	return s.listTrash()
}
func (s *Store) Counts() (int, int, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.fault != nil {
		return 0, 0, 0, s.fault
	}
	return s.counts()
}
func (s *Store) BootstrapPending() (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.fault != nil {
		return false, s.fault
	}
	return s.bootstrapPending()
}
func (s *Store) UsesImage(url string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.fault != nil {
		return false, s.fault
	}
	return s.usesImage(url)
}
func (s *Store) ImportedKeys(ctx context.Context, source string) (map[string]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.fault != nil {
		return nil, s.fault
	}
	return s.importedKeys(ctx, source)
}
func (s *Store) Initialize(seed []Note) error {
	ids := make([]string, 0, len(seed))
	for _, n := range seed {
		ids = append(ids, n.ID)
	}
	return s.mutate(func() error {
		for _, n := range seed {
			if !n.IsEmpty() {
				if err := validID(n.ID); err != nil {
					return err
				}
			}
		}
		return s.initialize(seed)
	}, ids...)
}
func (s *Store) Upsert(n Note) (Note, error) {
	var saved Note
	err := s.mutate(func() error {
		if err := validID(n.ID); err != nil {
			return err
		}
		var err error
		saved, err = s.upsert(n)
		return err
	}, n.ID)
	return saved, err
}
func (s *Store) MoveToTrash(id string) error {
	return s.mutate(func() error {
		if err := validID(id); err != nil {
			return err
		}
		return s.moveToTrash(id)
	}, id)
}
func (s *Store) Restore(id string) error {
	return s.mutate(func() error {
		if err := validID(id); err != nil {
			return err
		}
		return s.restore(id)
	}, id)
}
func (s *Store) Import(ctx context.Context, source string, entries []ImportEntry) (int, error) {
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.Note.ID)
	}
	var count int
	err := s.mutate(func() error {
		for _, e := range entries {
			if err := validID(e.Note.ID); err != nil {
				return err
			}
		}
		var err error
		count, err = s.importNotes(ctx, source, entries)
		return err
	}, ids...)
	return count, err
}
func (s *Store) PermanentlyDelete(id, trashRoot string) (Note, error) {
	var n Note
	err := s.mutate(func() error {
		if err := validID(id); err != nil {
			return err
		}
		if s.entriesDir != "" {
			items, err := s.listTrashIDs([]string{id})
			if err != nil {
				return err
			}
			for _, item := range items {
				if item.ID == id {
					if err = s.prepareImages(trashRoot, item.Note); err != nil {
						return err
					}
					if err = atomicJSON(filepath.Join(trashRoot, id, "note.json"), item); err != nil {
						return err
					}
					n = item.Note
					_, err = s.db.Exec(`DELETE FROM trash_notes WHERE id=?`, id)
					return err
				}
			}
			return sql.ErrNoRows
		}
		var err error
		n, err = s.permanentlyDelete(id, trashRoot)
		return err
	}, id)
	return n, err
}

// Only changed documents enter a commit; editing one entry never rewrites the
// other entries or copies their content into a library-wide content file.
func fileDelta(before, after fileSnapshot) fileSnapshot {
	delta := fileSnapshot{Meta: after.Meta}
	old := map[string]Note{}
	next := map[string]bool{}
	for _, n := range before.Notes {
		old[n.ID] = n
	}
	for _, n := range after.Notes {
		next[n.ID] = true
		if prev, ok := old[n.ID]; !ok || !reflect.DeepEqual(prev, n) {
			delta.Notes = append(delta.Notes, n)
		}
	}
	for _, n := range before.Notes {
		if !next[n.ID] {
			delta.Deletes = append(delta.Deletes, fileLocation{ID: n.ID})
		}
	}
	trash := map[string]TrashNote{}
	next = map[string]bool{}
	for _, n := range before.Trash {
		trash[n.ID] = n
	}
	for _, n := range after.Trash {
		next[n.ID] = true
		if prev, ok := trash[n.ID]; !ok || !reflect.DeepEqual(prev, n) {
			delta.Trash = append(delta.Trash, n)
		}
	}
	for _, n := range before.Trash {
		if !next[n.ID] {
			delta.Deletes = append(delta.Deletes, fileLocation{ID: n.ID, Trash: true})
		}
	}
	return delta
}

func idFilter(ids []string, column string) (string, []any) {
	// Avoid SQLite parameter limits on very large import batches.
	if len(ids) == 0 || len(ids) > 1000 {
		return "", nil
	}
	marks := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		marks[i] = "?"
		args[i] = id
	}
	return " WHERE " + column + " IN (" + strings.Join(marks, ",") + ")", args
}
