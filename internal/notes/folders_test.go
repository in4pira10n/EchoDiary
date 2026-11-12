package notes

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFoldersPersistWithoutMovingEntries(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".echodiary")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	if _, err := s.Upsert(Note{ID: "journal", Body: "keep this diary"}); err != nil {
		t.Fatal(err)
	}
	entry, _ := s.EntryDirectory("journal")
	for _, action := range []FolderAction{
		{Action: "create", ID: "first", Name: "旅行"},
		{Action: "create", ID: "empty", Name: "空文件夹"},
		{Action: "move", ID: "first", NoteIDs: []string{"journal", "journal"}},
		{Action: "rename", ID: "first", Name: "回忆"},
	} {
		if _, err := s.UpdateFolders(action); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	folders, err := s.Folders()
	if err != nil || len(folders) != 3 || folders[1].Name != "回忆" || !reflect.DeepEqual(folders[1].NoteIDs, []string{"journal"}) {
		t.Fatalf("folders after reopen: %#v, %v", folders, err)
	}
	if _, err := s.UpdateFolders(FolderAction{Action: "move", ID: "empty", NoteIDs: []string{"journal"}}); err != nil {
		t.Fatal(err)
	}
	folders, _ = s.Folders()
	if len(folders[1].NoteIDs) != 0 || len(folders[2].NoteIDs) != 1 {
		t.Fatal("move did not remove previous membership")
	}
	if err := s.MoveToTrash("journal"); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore("journal"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateFolders(FolderAction{Action: "delete", ID: "empty"}); err != nil {
		t.Fatal(err)
	}
	items, err := s.List()
	if err != nil || len(items) != 1 || items[0].Body != "keep this diary" {
		t.Fatalf("folder deletion changed notes: %#v, %v", items, err)
	}
	if current, _ := s.EntryDirectory("journal"); current != entry {
		t.Fatal("folder actions moved entry path")
	}
}

func TestInvalidFolderActionsPreserveMetadata(t *testing.T) {
	s, dir := openFilesTest(t)
	if _, err := s.UpdateFolders(FolderAction{Action: "create", ID: "f", Name: "Keep"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "folders.json"))
	for _, action := range []FolderAction{
		{Action: "create", ID: "f", Name: "Duplicate"},
		{Action: "rename", ID: "f", Name: "  "},
		{Action: "delete", ID: "missing"},
		{Action: "move", ID: "missing", NoteIDs: []string{"n"}},
		{Action: "unknown"},
		{Action: "create", ID: HandoffFolderID, Name: "duplicate"},
		{Action: "rename", ID: HandoffFolderID, Name: "changed"},
		{Action: "delete", ID: HandoffFolderID},
	} {
		if _, err := s.UpdateFolders(action); err == nil {
			t.Fatalf("accepted invalid action: %#v", action)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "folders.json"))
	if string(before) != string(after) {
		t.Fatal("invalid action changed metadata")
	}
}

func TestHandoffFolderMembershipPersists(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".echodiary")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Upsert(Note{ID: "relay", Body: "original"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateFolders(FolderAction{Action: "move", ID: HandoffFolderID, NoteIDs: []string{"relay"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.AppendHandoff("relay", "after restart", "text", "key", "request"); err != nil {
		t.Fatalf("lost handoff membership after reopen: %v", err)
	}
}
