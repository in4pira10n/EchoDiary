package notes

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAppendPreservesContentAndDurableReceipts(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), ".echodiary")
	s, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.Upsert(Note{ID: "entry", Title: "Keep", BodyHTML: "<h2><strong>Heading</strong></h2><p>Before</p>", Favorite: true, Images: []string{}, CreatedAt: "2025-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	appended, duplicate, err := s.Append(n.ID, "**literal** <script>bad</script>", "text", "key", "retry")
	if err != nil || duplicate {
		t.Fatalf("append: duplicate=%v err=%v", duplicate, err)
	}
	if !strings.Contains(appended.BodyHTML, "<h2><strong>Heading</strong></h2>") || !strings.Contains(appended.Body, "**literal** <script>bad</script>") || strings.Contains(appended.BodyHTML, "<script>") || !appended.Favorite || appended.CreatedAt != n.CreatedAt {
		t.Fatalf("lost content: %+v", appended)
	}
	again, duplicate, err := s.Append(n.ID, "**literal** <script>bad</script>", "text", "key", "retry")
	if err != nil || !duplicate || again.BodyHTML != appended.BodyHTML || again.UpdatedAt != appended.UpdatedAt {
		t.Fatalf("retry: %+v %v %v", again, duplicate, err)
	}
	if _, _, err = s.Append(n.ID, "different", "text", "key", "retry"); !errors.Is(err, ErrAppendConflict) {
		t.Fatalf("conflict: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, duplicate, err = s.Append(n.ID, "**literal** <script>bad</script>", "text", "key", "retry"); err != nil || !duplicate {
		t.Fatalf("reopened receipt: %v %v", duplicate, err)
	}
	if _, _, err = s.Append(n.ID, "**bold**", "markdown", "key", "next"); err != nil {
		t.Fatal(err)
	}
	items, _ := s.List()
	if !strings.Contains(items[0].BodyHTML, "<strong>bold</strong>") {
		t.Fatal(items[0].BodyHTML)
	}
	if err = s.MoveToTrash(n.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Append(n.ID, "Never resurrect", "text", "key", "deleted"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("trash: %v", err)
	}
}

func TestAppendMarkdownAndConcurrentWriters(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".echodiary"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Upsert(Note{ID: "md", BodyFormat: "markdown", BodyMarkdown: "# Heading\n\n**Original**"})
	if err != nil {
		t.Fatal(err)
	}
	n, _, err := s.Append("md", "# literal\n**stars**", "text", "key", "plain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(n.BodyHTML, "<h1>") != 1 || strings.Count(n.BodyHTML, "<strong>") != 1 || !strings.Contains(n.Body, "**stars**") {
		t.Fatal(n.BodyHTML)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := s.Append("md", fmt.Sprintf("chunk-%02d", i), "text", "key", fmt.Sprint(i))
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	items, _ := s.List()
	for i := 0; i < 12; i++ {
		if strings.Count(items[0].Body, fmt.Sprintf("chunk-%02d", i)) != 1 {
			t.Fatal(items[0].Body)
		}
	}
}

func TestAppendPreservesChecklistState(t *testing.T) {
	store := openTestStore(t)
	for _, note := range []Note{
		{ID: "tasks-html", BodyHTML: `<ul><li><input type="checkbox" checked>Done</li><li><input type="checkbox">Pending</li></ul>`},
		{ID: "tasks-md", BodyFormat: "markdown", BodyMarkdown: "- [x] Done\n- [ ] Pending"},
	} {
		if _, err := store.Upsert(note); err != nil {
			t.Fatal(err)
		}
		for i, addition := range []struct{ text, format string }{{"Clipboard text", "text"}, {"- [ ] New task", "markdown"}} {
			got, duplicate, err := store.Append(note.ID, addition.text, addition.format, "shortcut", fmt.Sprintf("%s-%d", note.ID, i))
			if err != nil || duplicate {
				t.Fatalf("append %s: %v %v", note.ID, duplicate, err)
			}
			if strings.Count(got.BodyHTML, `type="checkbox"`) != i+2 || strings.Count(got.BodyHTML, "checked") != 1 || !strings.Contains(got.Body, "Done") || !strings.Contains(got.Body, "Pending") {
				t.Fatalf("checklist state changed: %s", got.BodyHTML)
			}
		}
	}
}
