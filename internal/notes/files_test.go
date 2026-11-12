package notes

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func openFilesTest(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".echodiary")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}
func entryTestDir(t *testing.T, entries, id string) string {
	t.Helper()
	dirs, err := os.ReadDir(entries)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, d := range dirs {
		var n Note
		if d.IsDir() && readJSON(filepath.Join(entries, d.Name(), "note.json"), &n) == nil && n.ID == id {
			return filepath.Join(entries, d.Name())
		}
	}
	return filepath.Join(entries, EntryDirectoryName(Note{ID: id}))
}

func removeIndex(t *testing.T, dir string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		err := os.Remove(filepath.Join(dir, "notes.sqlite"+suffix))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}
func TestFileMigrationAndIndexRebuild(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "legacy")
	legacy, err := Open(legacyDir)
	if err != nil {
		t.Fatal(err)
	}
	const name = "img-0123456789abcdef0123456789abcdef.png"
	image := []byte("identical attachment bytes")
	os.MkdirAll(filepath.Join(legacyDir, "images"), 0755)
	os.WriteFile(filepath.Join(legacyDir, "images", name), image, 0644)
	if _, err = legacy.Upsert(Note{ID: "rich", Title: "格式", BodyHTML: "<h2>标题 <strong>粗体</strong></h2>", BodyFormat: "html", Favorite: true, Images: []string{"/media/" + name}}); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Upsert(Note{ID: "trashed", Body: "回收站", Images: []string{"/media/" + name}}); err != nil {
		t.Fatal(err)
	}
	if err = legacy.MoveToTrash("trashed"); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Import(context.Background(), "kehua", []ImportEntry{{Key: "source-fingerprint", Note: Note{ID: "kehua-note", BodyMarkdown: "## 标题\n\n**正文**", BodyFormat: "markdown", CreatedAt: "原始时间"}}}); err != nil {
		t.Fatal(err)
	}
	before, err := legacy.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	dir := filepath.Join(root, ".echodiary")
	if err = os.Rename(legacyDir, dir); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.snapshot()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("migration changed content/order/provenance: %v", err)
	}
	if _, err = os.Stat(filepath.Join(entryTestDir(t, filepath.Join(root, "entries"), "rich"), "note.json")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "entries")); !os.IsNotExist(err) {
		t.Fatal("entries was placed inside .echodiary")
	}
	for _, p := range []string{filepath.Join(entryTestDir(t, filepath.Join(root, "entries"), "rich"), "images", name), filepath.Join(dir, "recycle-bin", "trashed", "images", name)} {
		data, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(data, image) {
			t.Fatal("attachment changed", err)
		}
	}
	// All reads must work without the old flat image directory or SQLite index.
	if err = os.RemoveAll(filepath.Join(dir, "images")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	removeIndex(t, dir)
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rebuilt, err := s.snapshot()
	if err != nil || !reflect.DeepEqual(before, rebuilt) {
		t.Fatalf("rebuild changed content/order/provenance: %v", err)
	}
	path, err := s.ImagePath(name)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !bytes.Equal(data, image) {
		t.Fatal("legacy media URL resolution failed")
	}
	count, err := s.Import(context.Background(), "kehua", []ImportEntry{{Key: "source-fingerprint", Note: Note{ID: "kehua-note", Body: "must not overwrite"}}})
	if err != nil || count != 0 {
		t.Fatal("rebuild lost deduplication", err)
	}
	if err = s.Restore("trashed"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(entryTestDir(t, filepath.Join(root, "entries"), "trashed"), "images", name))
	if err != nil || !bytes.Equal(data, image) {
		t.Fatal("restore lost image", err)
	}
}
func TestFileCRUDAndPermanentArchive(t *testing.T) {
	s, dir := openFilesTest(t)
	root := filepath.Dir(dir)
	n, err := s.Upsert(Note{ID: "first", Body: "content", Favorite: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Upsert(n); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(entryTestDir(t, filepath.Join(root, "entries"), "first"), "note.json"))
	var onDisk Note
	if err = json.Unmarshal(data, &onDisk); err != nil || !reflect.DeepEqual(onDisk, n) {
		t.Fatal("saved note differs from JSON", err)
	}
	if err = s.MoveToTrash("first"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(entryTestDir(t, filepath.Join(root, "entries"), "first"))); !os.IsNotExist(err) {
		t.Fatal("deleted note remains active")
	}
	if err = s.Restore("first"); err != nil {
		t.Fatal(err)
	}
	if err = s.MoveToTrash("first"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PermanentlyDelete("first", filepath.Join(dir, "trash")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "trash", "first", "note.json")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	removeIndex(t, dir)
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	items, _ := s.List()
	trash, _ := s.ListTrash()
	pending, _ := s.BootstrapPending()
	if len(items) != 0 || len(trash) != 0 || pending {
		t.Fatal("archived note resurrected or bootstrap reopened")
	}
}
func TestFileImportFailureAndUnsafeIDs(t *testing.T) {
	s, dir := openFilesTest(t)
	if count, err := s.Import(context.Background(), "kehua", []ImportEntry{{Key: "valid", Note: Note{ID: "valid", Body: "one"}}, {Key: "bad", Note: Note{ID: "../outside", Body: "two"}}}); err == nil || count != 0 {
		t.Fatal("accepted unsafe batch")
	}
	if _, err := os.Stat(filepath.Join(entryTestDir(t, filepath.Join(filepath.Dir(dir), "entries"), "valid"), "note.json")); !os.IsNotExist(err) {
		t.Fatal("partial import committed")
	}
	for _, id := range []string{"../x", `a\b`, "/absolute", "CON", "a:", "AUX.txt", "a."} {
		if _, err := s.Upsert(Note{ID: id, Body: "x"}); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	items, _ := s.List()
	if len(items) != 0 {
		t.Fatal("invalid batch changed index")
	}
}
func TestFileWriteFailureRollsBackCandidateIndex(t *testing.T) {
	s, dir := openFilesTest(t)
	entries := filepath.Join(filepath.Dir(dir), "entries")
	if _, err := s.Upsert(Note{ID: "saved", Body: "before"}); err != nil {
		t.Fatal(err)
	}
	// Fail attachment preparation before a durable commit exists.
	blocked := filepath.Join(entryTestDir(t, entries, "saved"), "images")
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(Note{ID: "saved", Body: "after"}); err == nil {
		t.Fatal("expected failure")
	}
	items, _ := s.List()
	if len(items) != 1 || items[0].Body != "before" {
		t.Fatal("failed save changed visible index")
	}
	data, _ := os.ReadFile(filepath.Join(entryTestDir(t, entries, "saved"), "note.json"))
	var n Note
	json.Unmarshal(data, &n)
	if n.Body != "before" {
		t.Fatal("failed save changed authoritative content")
	}
}
func TestFileInterruptedCommitRecovery(t *testing.T) {
	s, dir := openFilesTest(t)
	if _, err := s.Upsert(Note{ID: "old", Body: "before"}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.snapshot()
	after := before
	after.Notes = []Note{{ID: "new", Body: "recovered", Images: []string{}, CreatedAt: "time", UpdatedAt: "time"}}
	after.Meta.Order = []string{"new"}
	if err := atomicJSON(filepath.Join(dir, "pending-commit.json"), fileDelta(before, after)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	removeIndex(t, dir)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	items, _ := s.List()
	if len(items) != 1 || items[0].ID != "new" || items[0].Body != "recovered" {
		t.Fatal("commit was not recovered")
	}
	if _, err = os.Stat(filepath.Join(entryTestDir(t, filepath.Join(filepath.Dir(dir), "entries"), "old"))); !os.IsNotExist(err) {
		t.Fatal("old entry not removed during recovery")
	}
}
func TestFilesAuthoritativeAndMalformedJSONFailsClosed(t *testing.T) {
	s, dir := openFilesTest(t)
	if _, err := s.Upsert(Note{ID: "n", Body: "original"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(entryTestDir(t, filepath.Join(filepath.Dir(dir), "entries"), "n"), "note.json")
	s.Close()
	var n Note
	readJSON(path, &n)
	n.Body = "edited offline"
	if err := atomicJSON(path, n); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := s.List()
	if items[0].Body != n.Body {
		t.Fatal("SQLite overwrote file content")
	}
	s.Close()
	os.WriteFile(path, []byte("broken JSON"), 0644)
	if other, err := Open(dir); err == nil {
		other.Close()
		t.Fatal("silently accepted broken JSON")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "broken JSON" {
		t.Fatal("invalid JSON was overwritten")
	}
}
func TestFileLibrarySingleWriter(t *testing.T) {
	s, dir := openFilesTest(t)
	if other, err := Open(dir); err == nil {
		other.Close()
		t.Fatal("allowed concurrent writer")
	}
	s.Close()
	other, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
}

func TestFailedFileCommitBlocksReadsUntilRecovered(t *testing.T) {
	s, dir := openFilesTest(t)
	if _, err := s.Upsert(Note{ID: "n", Body: "before"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(entryTestDir(t, filepath.Join(filepath.Dir(dir), "entries"), "n"), "note.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(Note{ID: "n", Body: "after"}); err == nil {
		t.Fatal("expected blocked file commit")
	}
	if _, err := s.List(); err == nil {
		t.Fatal("exposed candidate database while file recovery was blocked")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(Note{ID: "n", Body: "after"}); err != nil {
		t.Fatal(err)
	}
	items, err := s.List()
	if err != nil || len(items) != 1 || items[0].Body != "after" {
		t.Fatal("did not recover after repair", err)
	}
}
func TestPortableIDCaseCollisionDoesNotOverwrite(t *testing.T) {
	s, dir := openFilesTest(t)
	if _, err := s.Upsert(Note{ID: "Diary", Body: "original"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(Note{ID: "diary", Body: "different"}); err == nil {
		t.Fatal("accepted case collision")
	}
	items, err := s.List()
	if err != nil || len(items) != 1 || items[0].Body != "original" {
		t.Fatal("collision changed index", err)
	}
	var n Note
	if err := readJSON(filepath.Join(entryTestDir(t, filepath.Join(filepath.Dir(dir), "entries"), "Diary"), "note.json"), &n); err != nil || n.Body != "original" {
		t.Fatal("collision overwrote file", err)
	}
}
func TestEditingEntryDoesNotRewriteOthers(t *testing.T) {
	s, dir := openFilesTest(t)
	for _, id := range []string{"first", "second"} {
		if _, err := s.Upsert(Note{ID: id, Body: id}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(entryTestDir(t, filepath.Join(filepath.Dir(dir), "entries"), "first"), "note.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Upsert(Note{ID: "second", Body: "edited"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("unrelated entry was rewritten", err)
	}
}

func TestEntryDirectoryName(t *testing.T) {
	const id = "550e8400-e29b-41d4-a716-446655440000"
	for _, date := range []string{"创建于：2025年12月15日 01:50:00", "创建于：2025 年 12 月 15 日 01:50:00", "2025-12-15T01:50:00+08:00", "2025-12-14T17:50:00Z"} {
		got := EntryDirectoryName(Note{ID: id, CreatedAt: date})
		if got != "1765734600000_"+id {
			t.Fatalf("unexpected folder for %q: %s", date, got)
		}
	}
	spaced := EntryDirectoryName(Note{ID: id, CreatedAt: "创建于：2022 年 3 月 9 日 20:10:41"})
	if spaced != EntryDirectoryName(Note{ID: id, CreatedAt: "2022-03-09T20:10:41+08:00"}) || strings.HasPrefix(spaced, "0_") {
		t.Fatalf("spaced historical creation date was not parsed: %s", spaced)
	}
	custom := EntryDirectoryName(Note{ID: "kehua-fingerprint", CreatedAt: "unknown"})
	if !validEntryFolder(custom, "kehua-fingerprint") || custom != EntryDirectoryName(Note{ID: "kehua-fingerprint"}) || custom == EntryDirectoryName(Note{ID: "other"}) {
		t.Fatal("legacy ID mapping not stable/unique")
	}
}

func TestLegacyEntryFolderRenamePreservesFilesAndRecovery(t *testing.T) {
	s, dir := openFilesTest(t)
	n, err := s.Upsert(Note{ID: "550e8400-e29b-41d4-a716-446655440000", Body: "preserve", CreatedAt: "2025-12-15T01:50:00+08:00"})
	if err != nil {
		t.Fatal(err)
	}
	folder, err := s.EntryDirectory(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(filepath.Join(folder, "note.json"))
	if err = os.WriteFile(filepath.Join(folder, "images", "extra.dat"), []byte("attachment"), 0644); err != nil {
		t.Fatal(err)
	}
	s.Close()
	old := filepath.Join(filepath.Dir(dir), "entries", n.ID)
	if err = os.Rename(folder, old); err != nil {
		t.Fatal(err)
	}
	removeIndex(t, dir)
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.EntryDirectory(n.ID)
	if err != nil || got != folder {
		t.Fatal("legacy folder not migrated", err)
	}
	after, _ := os.ReadFile(filepath.Join(got, "note.json"))
	extra, _ := os.ReadFile(filepath.Join(got, "images", "extra.dat"))
	if !bytes.Equal(content, after) || string(extra) != "attachment" {
		t.Fatal("rename altered files")
	}
	if _, err = os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old folder remains")
	}
	n.CreatedAt = "2030-01-01T00:00:00Z"
	if _, err = s.Upsert(n); err != nil {
		t.Fatal(err)
	}
	stable, _ := s.EntryDirectory(n.ID)
	if stable != folder {
		t.Fatal("editing renamed existing folder")
	}
	s.Close()
	removeIndex(t, dir)
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	stable, _ = s.EntryDirectory(n.ID)
	if stable != folder {
		t.Fatal("reopening changed assigned directory")
	}
}

func TestLegacyEntryRenameRefusesOccupiedTarget(t *testing.T) {
	s, dir := openFilesTest(t)
	n, err := s.Upsert(Note{ID: "legacy", Body: "original", CreatedAt: "2025-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	folder, _ := s.EntryDirectory(n.ID)
	s.Close()
	if err = os.Rename(folder, filepath.Join(filepath.Dir(dir), "entries", n.ID)); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(folder, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(folder, "keep.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if other, err := Open(dir); err == nil {
		other.Close()
		t.Fatal("overwrote occupied target")
	}
	if data, err := os.ReadFile(filepath.Join(folder, "keep.txt")); err != nil || string(data) != "keep" {
		t.Fatal("target data changed", err)
	}
}
