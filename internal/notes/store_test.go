package notes

import (
	"os"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func TestInitializeImportsOnlyOnce(t *testing.T) {
	store := openTestStore(t)
	pending, err := store.BootstrapPending()
	if err != nil || !pending {
		t.Fatalf("bootstrap pending = %v, err = %v; want true, nil", pending, err)
	}

	first := Note{ID: "first", Title: "First", Body: "Body", Images: []string{"cover"}}
	if err := store.Initialize([]Note{first}); err != nil {
		t.Fatal(err)
	}
	if err := store.Initialize([]Note{{ID: "second", Title: "Second"}}); err != nil {
		t.Fatal(err)
	}

	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != first.ID || items[0].Title != first.Title {
		t.Fatalf("notes after repeated initialization = %#v; want only first note", items)
	}
	pending, err = store.BootstrapPending()
	if err != nil || pending {
		t.Fatalf("bootstrap pending = %v, err = %v; want false, nil", pending, err)
	}
}

func TestInitializePreservesExistingNote(t *testing.T) {
	store := openTestStore(t)
	existing := Note{ID: "existing", Title: "Already saved", Images: []string{}}
	if _, err := store.Upsert(existing); err != nil {
		t.Fatal(err)
	}
	if err := store.Initialize([]Note{{ID: "seed", Title: "Do not import"}}); err != nil {
		t.Fatal(err)
	}

	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != existing.ID {
		t.Fatalf("notes after initialization = %#v; want existing note preserved", items)
	}
	pending, err := store.BootstrapPending()
	if err != nil || pending {
		t.Fatalf("bootstrap pending = %v, err = %v; want false, nil", pending, err)
	}
}

func TestInitializeRejectsInvalidSeedAtomically(t *testing.T) {
	store := openTestStore(t)
	if err := store.Initialize([]Note{{Title: "Missing id"}}); err == nil {
		t.Fatal("Initialize accepted a note without an id")
	}

	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	pending, stateErr := store.BootstrapPending()
	if len(items) != 0 || stateErr != nil || !pending {
		t.Fatalf("after failed initialization: notes=%#v pending=%v err=%v", items, pending, stateErr)
	}
}

func TestInitializeSkipsEmptySeedNotes(t *testing.T) {
	store := openTestStore(t)
	if err := store.Initialize([]Note{
		{ID: "empty", Title: " \n", Body: "\t", Images: []string{" "}},
		{ID: "kept", Title: "A real note", Images: []string{}},
	}); err != nil {
		t.Fatal(err)
	}

	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "kept" {
		t.Fatalf("notes after initialization = %#v; want only the non-empty note", items)
	}
}

func TestUpsertDoesNotPersistEmptyNote(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.Upsert(Note{ID: "empty", Title: "Temporary", Body: "Text", Images: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert(Note{ID: "empty", Title: " \n", Body: "\t", Images: []string{" "}}); err != nil {
		t.Fatal(err)
	}
	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("stored notes = %#v; want no empty note", items)
	}
}

func TestUsesImageTracksSavedReferences(t *testing.T) {
	store := openTestStore(t)
	const imageURL = "/media/img-0123456789abcdef.jpg"
	if used, err := store.UsesImage(imageURL); err != nil || used {
		t.Fatalf("UsesImage before save = %v, err = %v; want false, nil", used, err)
	}
	if _, err := store.Upsert(Note{ID: "with-image", Title: "Photo", Images: []string{imageURL}}); err != nil {
		t.Fatal(err)
	}
	if used, err := store.UsesImage(imageURL); err != nil || !used {
		t.Fatalf("UsesImage after save = %v, err = %v; want true, nil", used, err)
	}
	if err := store.Delete("with-image"); err != nil {
		t.Fatal(err)
	}
	if used, err := store.UsesImage(imageURL); err != nil || used {
		t.Fatalf("UsesImage after delete = %v, err = %v; want false, nil", used, err)
	}
}

func TestTrashRestoreAndPermanentDelete(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.Upsert(Note{ID: "trash-me", Title: "A note", Body: "Content"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("trash-me"); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListTrash()
	if err != nil || len(items) != 1 || items[0].ID != "trash-me" {
		t.Fatalf("trash=%#v err=%v", items, err)
	}
	if err := store.Restore("trash-me"); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListTrash()
	if err != nil || len(items) != 0 {
		t.Fatalf("trash after restore=%#v err=%v", items, err)
	}
	if err := store.Delete("trash-me"); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "trash")
	if _, err := store.PermanentlyDelete("trash-me", root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "trash-me", "note.json")); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRemovesLegacyEmptyNotes(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO notes (id,title,body,images_json) VALUES ('legacy-empty','','','[]')`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	})
	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("notes after reopen = %#v; want legacy empty note removed", items)
	}
	pending, err := store.BootstrapPending()
	if err != nil || !pending {
		t.Fatalf("bootstrap pending = %v, err = %v; want true, nil after cleaning the only empty row", pending, err)
	}
}
