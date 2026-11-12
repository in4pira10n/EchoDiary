package server

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"echodiary/internal/notes"
)

func TestKehuaParserPreservesBody(t *testing.T) {
	text := "\ufeff2023年06月01日 01:56:03\r\n  正文第一段\r\n\r\n正文第二段\r\n[图片：photo.jpeg]\r\n\r\n\r\n2023年06月02日 02:18:30\r\n下一条\r\n"
	entries, err := parseKehuaEntries([]byte(text), "2023年-动态内容.txt")
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}
	if entries[0].Body != "  正文第一段\n\n正文第二段\n[图片：photo.jpeg]" {
		t.Fatalf("body changed: %q", entries[0].Body)
	}
	if entries[0].Timestamp.Second() != 3 {
		t.Fatal("lost source timestamp precision")
	}
	if _, err := parseKehuaEntries([]byte("2023年02月30日 01:56:03\ninvalid"), "bad.txt"); err == nil {
		t.Fatal("accepted invalid date")
	}
}

func writeKehuaFixture(t *testing.T, root, name string, data []byte) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKehuaImportImagesDuplicatesAndWarnings(t *testing.T) {
	root, dataDir := filepath.Join(t.TempDir(), ".echodiary"), filepath.Join(t.TempDir(), ".echodiary")
	store, err := notes.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	text := "2023年06月01日 01:56:03\n第一段\n\n第二段\n[图片：photo.jpeg]\n\n\n2023年06月02日 02:18:30\n[图片：missing.jpeg]\n[视频：movie.mp4]\n"
	writeKehuaFixture(t, root, "我的动态/2023年/2023年-动态内容.txt", []byte(text))
	var photo bytes.Buffer
	if err := jpeg.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	writeKehuaFixture(t, root, "我的动态/2023年/图片&视频/6月/photo.jpeg", photo.Bytes())
	imageDir := filepath.Join(dataDir, "images")
	report, err := importKehua(context.Background(), root, imageDir, store)
	if err != nil || report.Total != 2 || report.Imported != 2 || report.Images != 1 || len(report.Warnings) != 2 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	items, err := store.List()
	if err != nil || len(items) != 2 {
		t.Fatalf("notes=%d err=%v", len(items), err)
	}
	for _, note := range items {
		if note.Title != "" {
			t.Fatal("import created a title")
		}
		if len(note.Images) == 1 {
			if !strings.HasPrefix(note.Body, "第一段\n\n第二段\n[[image:/media/img-") {
				t.Fatalf("image position/body changed: %q", note.Body)
			}
			stored, err := os.ReadFile(filepath.Join(imageDir, strings.TrimPrefix(note.Images[0], "/media/")))
			if err != nil || !bytes.Equal(stored, photo.Bytes()) {
				t.Fatal("image not copied intact")
			}
		} else if note.Body != "[图片：missing.jpeg]\n[视频：movie.mp4]" {
			t.Fatal("lost unsupported/missing attachment references")
		}
	}
	edited := items[0]
	edited.Body = "用户修改后的正文"
	if _, err := store.Upsert(edited); err != nil {
		t.Fatal(err)
	}
	// Moving the package must not change duplicate detection.
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	report, err = importKehua(context.Background(), moved, imageDir, store)
	if err != nil || report.Imported != 0 || report.Skipped != 2 || report.Images != 0 {
		t.Fatalf("duplicate report=%+v err=%v", report, err)
	}
	items, err = store.List()
	if err != nil || len(items) != 2 || items[0].Body != edited.Body {
		t.Fatal("reimport overwrote an edited note")
	}

	// Rebuild the index using only per-entry JSON/images and durable provenance.
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err = os.Remove(filepath.Join(dataDir, "notes.sqlite"+suffix)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if err = os.RemoveAll(imageDir); err != nil {
		t.Fatal(err)
	}
	store, err = notes.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report, err = importKehua(context.Background(), moved, imageDir, store)
	if err != nil || report.Imported != 0 || report.Skipped != 2 {
		t.Fatalf("dedup after rebuild: %+v %v", report, err)
	}
	items, err = store.List()
	if err != nil || len(items) != 2 || items[0].Body != edited.Body {
		t.Fatal("rebuild changed edited content")
	}
	for _, n := range items {
		for _, url := range n.Images {
			path, err := store.ImagePath(strings.TrimPrefix(url, "/media/"))
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, photo.Bytes()) {
				t.Fatal("entry attachment changed", err)
			}
		}
	}
}

func TestKehuaImportRollsBackImagesOnDatabaseFailure(t *testing.T) {
	root, dataDir := filepath.Join(t.TempDir(), ".echodiary"), filepath.Join(t.TempDir(), ".echodiary")
	store, err := notes.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	text := []byte("2023年06月01日 01:56:03\n正文\n[图片：photo.jpeg]")
	entries, err := parseKehuaEntries(text, "2023年/2023年-动态内容.txt")
	if err != nil {
		t.Fatal(err)
	}
	// Force an ID conflict to fail the transaction after images are copied.
	if _, err := store.Upsert(notes.Note{ID: "kehua-" + entries[0].Key, Body: "已有日记"}); err != nil {
		t.Fatal(err)
	}
	writeKehuaFixture(t, root, "我的动态/2023年/2023年-动态内容.txt", text)
	var photo bytes.Buffer
	_ = jpeg.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil)
	writeKehuaFixture(t, root, "我的动态/2023年/图片&视频/6月/photo.jpeg", photo.Bytes())
	imageDir := filepath.Join(dataDir, "images")
	if _, err := importKehua(context.Background(), root, imageDir, store); err == nil {
		t.Fatal("expected database conflict")
	}
	files, err := os.ReadDir(imageDir)
	if err != nil || len(files) != 0 {
		t.Fatal("failed import left copied images")
	}
	keys, err := store.ImportedKeys(context.Background(), "kehua")
	if err != nil || len(keys) != 0 {
		t.Fatal("failed import retained duplicate markers")
	}
	items, err := store.List()
	if err != nil || len(items) != 1 || items[0].Body != "已有日记" {
		t.Fatal("failed import modified existing notes")
	}
}

// Optional read-only verification against a real package; all output goes to a
// temporary library, never to the user's active library or the source package.
func TestKehuaExportPackage(t *testing.T) {
	source := os.Getenv("ECHODIARY_TEST_KEHUA")
	if source == "" {
		t.Skip("set ECHODIARY_TEST_KEHUA to verify an export")
	}
	dataDir := filepath.Join(t.TempDir(), ".echodiary")
	store, err := notes.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report, err := importKehua(context.Background(), source, filepath.Join(dataDir, "images"), store)
	if err != nil || report.Imported == 0 {
		t.Fatalf("import failed: %v", err)
	}
	t.Logf("entries=%d imported=%d images=%d warnings=%d", report.Total, report.Imported, report.Images, len(report.Warnings))
	if len(report.Warnings) != 0 {
		t.Errorf("sample export has %d attachment warnings", len(report.Warnings))
	}
	repeat, err := importKehua(context.Background(), source, filepath.Join(dataDir, "images"), store)
	if err != nil || repeat.Imported != 0 || repeat.Skipped != report.Imported {
		t.Fatalf("reimport failed: %+v %v", repeat, err)
	}
}
