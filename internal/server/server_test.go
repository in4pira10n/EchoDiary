package server

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"echodiary/internal/library"
	"echodiary/web"
)

func TestImageFormat(t *testing.T) {
	tests := []struct {
		name, wantExtension, wantType string
		data                          []byte
	}{
		{"jpeg", ".jpg", "image/jpeg", []byte{0xff, 0xd8, 0xff, 0x00}},
		{"png", ".png", "image/png", []byte("\x89PNG\r\n\x1a\n")},
		{"gif", ".gif", "image/gif", []byte("GIF89a")},
		{"webp", ".webp", "image/webp", []byte("RIFF0000WEBP")},
		{"avif", ".avif", "image/avif", []byte("0000ftypavif")},
		{"heic", ".heic", "image/heic", []byte("0000ftypheic")},
		{"bmp", ".bmp", "image/bmp", []byte("BM00")},
		{"unknown", "", "", []byte("not an image")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotExtension, gotType := imageFormat(test.data)
			if gotExtension != test.wantExtension || gotType != test.wantType {
				t.Fatalf("imageFormat() = %q, %q; want %q, %q", gotExtension, gotType, test.wantExtension, test.wantType)
			}
		})
	}
}

func TestPWAShellFiles(t *testing.T) {
	assets, err := fs.Sub(web.Files, "static")
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"index.html", "manifest.webmanifest", "pwa.js", "sw.js", "app.css", "app.js"} {
		if _, err := fs.ReadFile(assets, name); err != nil {
			t.Errorf("PWA shell asset %q is unavailable: %v", name, err)
		}
	}

	serviceWorker, err := fs.ReadFile(assets, "sw.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serviceWorker), "const APP_KIT_ASSETS") || !strings.Contains(string(serviceWorker), "url.pathname.startsWith('/api/')") || !strings.Contains(string(serviceWorker), "url.pathname.startsWith('/media/')") {
		t.Fatal("service worker must cache EchoKit shell dependencies and bypass API and media requests")
	}
	if strings.Contains(string(serviceWorker), "startsWith('/kit/')") {
		t.Fatal("service worker must not bypass EchoKit shell dependencies")
	}

	manifest, err := fs.ReadFile(assets, "manifest.webmanifest")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(manifest, &payload); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if payload["display"] != "standalone" {
		t.Fatal("PWA manifest must use standalone display mode, matching EchoGallery")
	}

	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range []string{
		`<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">`,
		`<meta name="apple-mobile-web-app-capable" content="yes">`,
		`<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">`,
	} {
		if !strings.Contains(string(index), meta) {
			t.Fatalf("PWA shell must include EchoGallery's iOS configuration: %s", meta)
		}
	}
}

func TestValidImageName(t *testing.T) {
	if !validImageName("img-0123456789abcdef0123456789abcdef.webp") {
		t.Fatal("valid generated image name rejected")
	}
	for _, name := range []string{
		"../img-0123456789abcdef0123456789abcdef.png",
		"img-0123456789abcdef0123456789abcdef.svg",
		"img-short.png",
		"img-0123456789abcdef0123456789abcdef.png/other",
	} {
		if validImageName(name) {
			t.Errorf("validImageName(%q) = true; want false", name)
		}
	}
}

func TestLibraryInfoHandlerDoesNotExposeHostPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-host-folder-name")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	layout, err := library.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/library", nil)
	response := httptest.NewRecorder()
	libraryInfoHandler(layout)(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("library info status = %d; want 200", response.Code)
	}
	if strings.Contains(response.Body.String(), root) || strings.Contains(response.Body.String(), layout.DataDir) {
		t.Fatalf("library info leaked a host path: %s", response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["id"] != layout.Manifest.ID || payload["name"] != filepath.Base(root) || payload["dataFolder"] != ".echodiary" {
		t.Fatalf("library info = %#v; got unexpected metadata", payload)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/library", nil)
	response = httptest.NewRecorder()
	libraryInfoHandler(layout)(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("non-GET library info status = %d; want 405", response.Code)
	}
}

func TestImageUploadHandlerStoresImageWithGeneratedName(t *testing.T) {
	imageDir := t.TempDir()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "photo.png")
	if err != nil {
		t.Fatal(err)
	}
	imageData := []byte("\x89PNG\r\n\x1a\nimage bytes")
	if _, err := part.Write(imageData); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/images", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	imageUploadHandler(imageDir)(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body = %q; want 200", response.Code, response.Body.String())
	}
	var payload struct {
		URL         string `json:"url"`
		ContentType string `json:"contentType"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(payload.URL, "/media/img-") || !strings.HasSuffix(payload.URL, ".png") || payload.ContentType != "image/png" {
		t.Fatalf("upload response = %#v; want generated PNG URL", payload)
	}
	name := strings.TrimPrefix(payload.URL, "/media/")
	if !validImageName(name) {
		t.Fatalf("generated image name %q is invalid", name)
	}
	saved, err := os.ReadFile(filepath.Join(imageDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, imageData) {
		t.Fatalf("stored image = %q; want %q", saved, imageData)
	}
}

func TestImageUploadHandlerRejectsNonImage(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "fake.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("not an image")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/images", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	imageUploadHandler(t.TempDir())(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("upload status = %d, body = %q; want 415", response.Code, response.Body.String())
	}
}
