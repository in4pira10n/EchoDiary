package notes

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRichTextRoundTripAndTrash(t *testing.T) {
	store := openTestStore(t)
	saved, err := store.Upsert(Note{ID: "rich", BodyHTML: `<p>中文 <strong>bold</strong> <em>italic</em> <u>underlined</u> <del>gone</del></p><ul><li>one</li></ul>`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved.Body, "中文 bold italic underlined gone") || strings.Contains(saved.Body, "<strong>") {
		t.Fatalf("plain body: %q", saved.Body)
	}
	assertHTML := func(got Note) {
		t.Helper()
		if got.BodyHTML != saved.BodyHTML || got.Body != saved.Body {
			t.Fatalf("content changed: %#v", got)
		}
	}
	items, err := store.List()
	if err != nil || len(items) != 1 {
		t.Fatalf("list: %#v %v", items, err)
	}
	assertHTML(items[0])
	// A format-only edit must count as a content modification.
	time.Sleep(time.Millisecond)
	saved.BodyHTML = strings.ReplaceAll(saved.BodyHTML, "strong", "b")
	changed, err := store.Upsert(saved)
	if err != nil || changed.UpdatedAt == saved.UpdatedAt {
		t.Fatalf("format change timestamp: %#v %v", changed, err)
	}
	saved = changed
	if err := store.MoveToTrash(saved.ID); err != nil {
		t.Fatal(err)
	}
	trash, err := store.ListTrash()
	if err != nil || len(trash) != 1 {
		t.Fatalf("trash: %#v %v", trash, err)
	}
	assertHTML(trash[0].Note)
	if err := store.Restore(saved.ID); err != nil {
		t.Fatal(err)
	}
	items, err = store.List()
	if err != nil || len(items) != 1 {
		t.Fatalf("restored: %#v %v", items, err)
	}
	assertHTML(items[0])
}

func TestMarkdownSourceAndSafeRendering(t *testing.T) {
	store := openTestStore(t)
	source := "# 日记\n\n**加粗** 和 *斜体*，~~删除~~\n\n- [x] 完成\n- 下一项\n\n```go\nfmt.Println(1)\n```\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n<script>alert(1)</script>\n\n[危险](javascript:alert(1))"
	saved, err := store.Upsert(Note{ID: "md", BodyFormat: "markdown", BodyMarkdown: source, Body: "untrusted summary", BodyHTML: "<p>untrusted preview</p>"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.BodyMarkdown != source || saved.BodyFormat != "markdown" {
		t.Fatalf("lost source: %#v", saved)
	}
	for _, tag := range []string{"<h1>", "<strong>", "<em>", "<del>", "<ul>", "<pre>", "<table>"} {
		if !strings.Contains(saved.BodyHTML, tag) {
			t.Errorf("missing %s: %s", tag, saved.BodyHTML)
		}
	}
	for _, bad := range []string{"<script", "javascript:", "untrusted"} {
		if strings.Contains(saved.BodyHTML, bad) || strings.Contains(saved.Body, bad) {
			t.Errorf("unsafe or stale content %q: %#v", bad, saved)
		}
	}
	if err := store.MoveToTrash(saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Restore(saved.ID); err != nil {
		t.Fatal(err)
	}
	items, _ := store.List()
	if len(items) != 1 || items[0].BodyMarkdown != source || items[0].BodyHTML != saved.BodyHTML {
		t.Fatalf("lost Markdown after restore: %#v", items)
	}
}

func TestContentSanitizationAndManagedImages(t *testing.T) {
	url := "/media/img-0123456789abcdef0123456789abcdef.png"
	n := normalizeContent(Note{BodyHTML: `<p onclick="alert(1)">safe <strong>bold</strong></p><script>evil()</script><svg onload="evil()"></svg><iframe src="evil"></iframe><a href="javascript:evil()">link</a><img src="` + url + `" onerror="evil()"><input autofocus onfocus="evil()">`})
	for _, bad := range []string{"onclick", "onerror", "onfocus", "autofocus", "evil", "<svg", "<iframe", "<script", "javascript:"} {
		if strings.Contains(n.BodyHTML, bad) {
			t.Errorf("retained %q: %s", bad, n.BodyHTML)
		}
	}
	if !strings.Contains(n.BodyHTML, "<strong>bold</strong>") || !strings.Contains(n.Body, "[[image:"+url+"]]") || len(n.Images) != 1 || n.Images[0] != url {
		t.Fatalf("lost safe content: %#v", n)
	}
	store := openTestStore(t)
	if _, err := store.Upsert(Note{ID: "empty", BodyHTML: "<p><br></p><script>alert(1)</script>"}); err != nil {
		t.Fatal(err)
	}
	items, _ := store.List()
	if len(items) != 0 {
		t.Fatalf("persisted empty HTML: %#v", items)
	}
}

func TestLegacySchemaMigrationPreservesPlainText(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "notes.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE notes (id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '', body TEXT NOT NULL DEFAULT '', images_json TEXT NOT NULL DEFAULT '[]', source INTEGER NOT NULL DEFAULT 0, favorite INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT '');
	CREATE TABLE trash_notes (id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '', body TEXT NOT NULL DEFAULT '', images_json TEXT NOT NULL DEFAULT '[]', source INTEGER NOT NULL DEFAULT 0, favorite INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL DEFAULT '', deleted_at TEXT NOT NULL);
	INSERT INTO notes(id,body) VALUES('old','**literal text**');
	INSERT INTO trash_notes(id,body,deleted_at) VALUES('old-trash','# literal heading','2026-01-01');`)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	items, err := store.List()
	if err != nil || len(items) != 1 || items[0].Body != "**literal text**" || items[0].BodyHTML != "" {
		t.Fatalf("legacy notes: %#v %v", items, err)
	}
	trash, err := store.ListTrash()
	if err != nil || len(trash) != 1 || trash[0].Body != "# literal heading" {
		t.Fatalf("legacy trash: %#v %v", trash, err)
	}
	if err := store.Restore("old-trash"); err != nil {
		t.Fatal(err)
	}
}

func TestInitializePreservesRichContent(t *testing.T) {
	store := openTestStore(t)
	if err := store.Initialize([]Note{{ID: "rich-seed", BodyHTML: "<p><strong>seed</strong></p>"}}); err != nil {
		t.Fatal(err)
	}
	items, err := store.List()
	if err != nil || len(items) != 1 || items[0].BodyHTML != "<p><strong>seed</strong></p>" || items[0].Body != "seed" {
		t.Fatalf("seed: %#v %v", items, err)
	}
}

func TestChecklistRoundTrip(t *testing.T) {
	store := openTestStore(t)
	for _, n := range []Note{
		{ID: "tasks-html", BodyHTML: "<ul><li><input type=checkbox checked><strong>完成</strong></li><li><input type=checkbox>待办</li></ul>"},
		{ID: "tasks-md", BodyFormat: "markdown", BodyMarkdown: "- [x] 完成\n- [ ] 待办"},
		{ID: "tasks-empty", BodyHTML: "<ul><li><input type=checkbox></li></ul>"},
	} {
		saved, err := store.Upsert(n)
		if err != nil {
			t.Fatal(err)
		}
		if saved.IsEmpty() || !strings.Contains(saved.BodyHTML, `type="checkbox"`) || !strings.Contains(saved.BodyHTML, "disabled") {
			t.Fatalf("lost checklist: %#v", saved)
		}
		if n.ID != "tasks-empty" && !strings.Contains(saved.BodyHTML, "checked") {
			t.Fatalf("lost state: %#v", saved)
		}
		if err := store.MoveToTrash(saved.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.Restore(saved.ID); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.List()
	if err != nil || len(items) != 3 {
		t.Fatalf("checklists lost after restore: %#v %v", items, err)
	}
}

func TestParagraphAlignmentSurvivesSaveAndAppend(t *testing.T) {
	store := openTestStore(t)
	for _, format := range []string{"", "markdown"} {
		body := `<p align="center"><strong>居中正文</strong></p><p align="right">右对齐</p><p align="justify">两端对齐</p>`
		note := Note{ID: "aligned-" + format, BodyFormat: format, BodyHTML: body}
		if format == "markdown" {
			note.BodyMarkdown = body
		}
		saved, err := store.Upsert(note)
		if err != nil {
			t.Fatal(err)
		}
		appended, _, err := store.Append(saved.ID, "接力追加", "text", "shortcut", saved.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, alignment := range []string{"center", "right", "justify"} {
			if !strings.Contains(appended.BodyHTML, `align="`+alignment+`"`) {
				t.Fatalf("lost alignment %s: %s", alignment, appended.BodyHTML)
			}
		}
	}
	unsafe := normalizeContent(Note{BodyHTML: `<p align="invalid" style="position:fixed">Safe</p>`})
	if strings.Contains(unsafe.BodyHTML, "align=") || strings.Contains(unsafe.BodyHTML, "style=") || unsafe.Body != "Safe" {
		t.Fatalf("unsafe alignment: %#v", unsafe)
	}
}
