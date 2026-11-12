package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"echodiary/internal/library"
	"echodiary/internal/notes"
)

func TestSetupCreatesHashedAdminAndProtectsDiaryAPI(t *testing.T) {
	settings, err := openSettingsFile(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	layout, err := library.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := notes.Open(layout.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state, err := newDiaryRuntime(settings, &libraryHandle{layout: layout, store: store}, false)
	if err != nil {
		t.Fatal(err)
	}
	sessions := newSessions(settings)
	setup := setupHandler(settings, state, sessions)
	requestBody, err := json.Marshal(setupRequest{
		Username: "diary-admin", Password: "correct horse battery", LibraryName: "My Diary", LibraryPath: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	setupRequest := httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(requestBody))
	setupRequest.RemoteAddr = "192.168.1.25:43210"
	setup.ServeHTTP(response, setupRequest)
	if response.Code != http.StatusForbidden {
		t.Fatalf("remote setup status = %d, body = %q; want 403", response.Code, response.Body.String())
	}
	if settings.snapshot().Admin != nil {
		t.Fatal("remote setup created an administrator")
	}

	response = httptest.NewRecorder()
	setupRequest = httptest.NewRequest(http.MethodPost, "/api/auth/setup", bytes.NewReader(requestBody))
	setupRequest.RemoteAddr = "127.0.0.1:43210"
	setup.ServeHTTP(response, setupRequest)
	if response.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, body = %q; want 201", response.Code, response.Body.String())
	}
	cookie := response.Result().Cookies()[0]
	if cookie.Name != sessionCookie || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected session cookie: %#v", cookie)
	}
	saved, err := os.ReadFile(settings.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "correct horse battery") {
		t.Fatal("settings file stored the plain-text password")
	}
	if !verifyPassword(settings.snapshot().Admin, "DIARY-ADMIN", "correct horse battery") {
		t.Fatal("saved password hash did not verify")
	}
	restartedSettings, err := openSettingsFile(settings.path)
	if err != nil {
		t.Fatal(err)
	}

	protected := authRequired(restartedSettings, newSessions(restartedSettings), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	unauthenticated := httptest.NewRecorder()
	protected.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/notes", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API status = %d; want 401", unauthenticated.Code)
	}
	authenticated := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/notes", nil)
	request.AddCookie(cookie)
	protected.ServeHTTP(authenticated, request)
	if authenticated.Code != http.StatusNoContent {
		t.Fatalf("authenticated API status after reopening settings = %d; want 204", authenticated.Code)
	}
	tampered := httptest.NewRecorder()
	tamperedRequest := httptest.NewRequest(http.MethodGet, "/api/notes", nil)
	tamperedRequest.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie.Value + "x"})
	protected.ServeHTTP(tampered, tamperedRequest)
	if tampered.Code != http.StatusUnauthorized {
		t.Fatalf("tampered session status = %d; want 401", tampered.Code)
	}
}

func TestDiaryRuntimeAddsAndSwitchesLibrariesWithoutReplacingData(t *testing.T) {
	settings, err := openSettingsFile(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	firstRoot := t.TempDir()
	firstLayout, err := library.Open(firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	firstStore, err := notes.Open(firstLayout.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := firstStore.Upsert(notes.Note{ID: "kept", Title: "Keep me", Body: "First library"}); err != nil {
		t.Fatal(err)
	}
	state, err := newDiaryRuntime(settings, &libraryHandle{layout: firstLayout, store: firstStore}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer state.close()
	account, err := makeAdminAccount("admin", "a long enough password")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.installSetup(account, libraryRecord{ID: firstLayout.Manifest.ID, Name: firstLayout.Manifest.Name, Path: firstRoot}, state.handles[firstLayout.Manifest.ID]); err != nil {
		t.Fatal(err)
	}

	secondRoot := filepath.Join(t.TempDir(), "second-library")
	second, err := state.addLibrary(secondRoot, "Second Diary")
	if err != nil {
		t.Fatal(err)
	}
	activeLayout, secondStore, err := state.active()
	if err != nil {
		t.Fatal(err)
	}
	if activeLayout.Manifest.ID != second.ID || activeLayout.Manifest.Name != "Second Diary" {
		t.Fatalf("active library = %+v; wanted newly added library", activeLayout.Manifest)
	}
	if _, err := secondStore.Upsert(notes.Note{ID: "second", Title: "Separate", Body: "Second library"}); err != nil {
		t.Fatal(err)
	}
	if err := state.setFavorite(second.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := state.activate(firstLayout.Manifest.ID); err != nil {
		t.Fatal(err)
	}
	_, activeStore, err := state.active()
	if err != nil {
		t.Fatal(err)
	}
	items, err := activeStore.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "kept" {
		t.Fatalf("switching libraries lost or mixed notes: %+v", items)
	}
	cards := state.listLibraries()
	if len(cards) != 2 || !cards[1].Favorite {
		t.Fatalf("library cards = %+v; expected two entries and a saved star", cards)
	}
}

func TestDiaryRuntimeRelocatesOnlyMatchingLibrary(t *testing.T) {
	settings, err := openSettingsFile(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	layout, err := library.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := notes.Open(layout.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	state, err := newDiaryRuntime(settings, &libraryHandle{layout: layout, store: store}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer state.close()
	account, err := makeAdminAccount("admin", "a long enough password")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.installSetup(account, libraryRecord{ID: layout.Manifest.ID, Name: layout.Manifest.Name, Path: root}, state.handles[layout.Manifest.ID]); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(t.TempDir(), "relocated")
	if err := os.MkdirAll(filepath.Join(target, ".echodiary", "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, ".echodiary", "library.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, ".echodiary", "library.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.relocate(layout.Manifest.ID, target); err != nil {
		t.Fatal(err)
	}
	active, _, err := state.active()
	if err != nil {
		t.Fatal(err)
	}
	if active.Root != target {
		t.Fatalf("active library root = %q; want %q", active.Root, target)
	}
	if got := state.entries[0].Path; got != target {
		t.Fatalf("saved library path = %q; want %q", got, target)
	}

	otherRoot := t.TempDir()
	other, err := library.Open(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.relocate(layout.Manifest.ID, otherRoot); err == nil {
		t.Fatal("relocate accepted a different library manifest")
	}
	_ = other
}
