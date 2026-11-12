package library

import (
	"os"
	"path/filepath"
	"testing"

	"echodiary/internal/notes"
)

func TestOpenCreatesAndReusesLibraryIdentity(t *testing.T) {
	root := t.TempDir()
	first, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Manifest.ID == "" || first.Manifest.ID != second.Manifest.ID {
		t.Fatalf("library identity was not stable: first=%q second=%q", first.Manifest.ID, second.Manifest.ID)
	}
	if first.DataDir != filepath.Join(root, ".echodiary") || first.ImageDir != filepath.Join(first.DataDir, "images") {
		t.Fatalf("unexpected data layout: %+v", first)
	}
}

func TestCopyLegacyDataKeepsSourceAndNotes(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), ".echodiary-data")
	oldStore, err := notes.Open(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldStore.Upsert(notes.Note{ID: "note-1", Title: "A note", Body: "Kept"}); err != nil {
		t.Fatal(err)
	}
	if err := oldStore.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(legacy, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(legacy, "images", "img-test.png")
	if err := os.WriteFile(imagePath, []byte("image bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	layout, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	copied, err := CopyLegacyData(legacy, layout.DataDir)
	if err != nil || !copied {
		t.Fatalf("CopyLegacyData() = %v, %v; want true, nil", copied, err)
	}
	newStore, err := notes.Open(layout.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := newStore.List()
	if err != nil {
		t.Fatal(err)
	}
	if err := newStore.Close(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "note-1" || got[0].Body != "Kept" {
		t.Fatalf("copied notes = %+v", got)
	}
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("legacy image was removed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(layout.ImageDir, "img-test.png")); err != nil || string(data) != "image bytes" {
		t.Fatalf("copied image = %q, %v", data, err)
	}
}

func TestCopyLegacyDataDoesNotOverwriteExistingLibrary(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "legacy")
	oldStore, err := notes.Open(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldStore.Upsert(notes.Note{ID: "old", Body: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := oldStore.Close(); err != nil {
		t.Fatal(err)
	}

	layout, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	current, err := notes.Open(layout.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := current.Upsert(notes.Note{ID: "current", Body: "current"}); err != nil {
		t.Fatal(err)
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	copied, err := CopyLegacyData(legacy, layout.DataDir)
	if err != nil || copied {
		t.Fatalf("CopyLegacyData() = %v, %v; want false, nil", copied, err)
	}
	check, err := notes.Open(layout.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := check.List()
	_ = check.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "current" {
		t.Fatalf("existing library was overwritten: %+v", got)
	}
}

func TestMigrateLegacyDataOnceMarksOnlySuccessfulCopy(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), ".echodiary-data")
	oldStore, err := notes.Open(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldStore.Upsert(notes.Note{ID: "legacy-note", Body: "preserved"}); err != nil {
		t.Fatal(err)
	}
	if err := oldStore.Close(); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(t.TempDir(), ".echodiary-library-migration-v1")
	existing, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	existingStore, err := notes.Open(existing.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := existingStore.Close(); err != nil {
		t.Fatal(err)
	}
	copied, err := MigrateLegacyDataOnce(legacy, existing.DataDir, marker)
	if err != nil || copied {
		t.Fatalf("migration into a non-empty library = %v, %v; want false, nil", copied, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("skipped migration unexpectedly created marker: %v", err)
	}

	first, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	copied, err = MigrateLegacyDataOnce(legacy, first.DataDir, marker)
	if err != nil || !copied {
		t.Fatalf("first migration into an empty library = %v, %v; want true, nil", copied, err)
	}
	second, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	copied, err = MigrateLegacyDataOnce(legacy, second.DataDir, marker)
	if err != nil || copied {
		t.Fatalf("second migration = %v, %v; want false, nil", copied, err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "notes.sqlite")); err != nil {
		t.Fatalf("migration removed the legacy database: %v", err)
	}
}

func TestCopyLegacyDataRejectsConflictingImageName(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "legacy")
	oldStore, err := notes.Open(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldStore.Upsert(notes.Note{ID: "legacy-note", Body: "with image", Images: []string{"/media/img-shared.png"}}); err != nil {
		t.Fatal(err)
	}
	if err := oldStore.Close(); err != nil {
		t.Fatal(err)
	}
	legacyImages := filepath.Join(legacy, "images")
	if err := os.MkdirAll(legacyImages, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyImages, "img-shared.png"), []byte("legacy image"), 0o644); err != nil {
		t.Fatal(err)
	}
	layout, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conflict := filepath.Join(layout.ImageDir, "img-shared.png")
	if err := os.WriteFile(conflict, []byte("different image"), 0o644); err != nil {
		t.Fatal(err)
	}

	if copied, err := CopyLegacyData(legacy, layout.DataDir); err == nil || copied {
		t.Fatalf("conflicting image migration = %v, %v; want false and an error", copied, err)
	}
	if _, err := os.Stat(filepath.Join(layout.DataDir, "notes.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("database installed despite image collision: %v", err)
	}
	if data, err := os.ReadFile(conflict); err != nil || string(data) != "different image" {
		t.Fatalf("existing image was changed: %q, %v", data, err)
	}
}

func TestLegacyCopyDoesNotSeedRebuildableIndex(t *testing.T) {
	legacy := t.TempDir()
	old, err := notes.Open(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = old.Upsert(notes.Note{ID: "old", Body: "old"}); err != nil {
		t.Fatal(err)
	}
	old.Close()
	layout, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := notes.Open(layout.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Upsert(notes.Note{ID: "current", Body: "current"}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if err = os.Remove(filepath.Join(layout.DataDir, "notes.sqlite")); err != nil {
		t.Fatal(err)
	}
	for _, copyOnce := range []bool{false, true} {
		var copied bool
		if copyOnce {
			copied, err = MigrateLegacyDataOnce(legacy, layout.DataDir, filepath.Join(t.TempDir(), "marker"))
		} else {
			copied, err = CopyLegacyData(legacy, layout.DataDir)
		}
		if err != nil || copied {
			t.Fatal("legacy import confused missing index with empty library", err)
		}
	}
	current, err := notes.Open(layout.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	items, err := current.List()
	if err != nil || len(items) != 1 || items[0].ID != "current" {
		t.Fatal("rebuild lost file-backed entries", err)
	}
	if layout, err = Load(layout.Root); err != nil || layout.Manifest.Version != 2 {
		t.Fatal("file-backed manifest not version 2", err)
	}
}
