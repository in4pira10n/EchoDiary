package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"echodiary/internal/library"
	"echodiary/internal/notes"
)

func TestHandoffCapabilityScopeAndRevocation(t *testing.T) {
	settings, err := openSettingsFile(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := makeAdminAccount("tester", "test-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if err = settings.update(func(next *savedSettings) error { next.Admin = admin; return nil }); err != nil {
		t.Fatal(err)
	}
	layout, err := library.Open(t.TempDir())
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
	for _, id := range []string{"a", "b", "outside"} {
		if _, err = store.Upsert(notes.Note{ID: id, Title: "Same title", Body: "original"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.UpdateFolders(notes.FolderAction{Action: "move", ID: notes.HandoffFolderID, NoteIDs: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/handoff/keys", handoffKeysHandler(settings, state))
	mux.HandleFunc("/api/handoff/notes", handoffHandler(settings, state))
	mux.HandleFunc("/api/handoff/append", handoffHandler(settings, state))
	mux.HandleFunc("/api/notes", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	protected := authRequired(settings, newSessions(settings), mux)
	request := func(handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	create := func(note string) (string, string) {
		w := request(mux, "POST", "/api/handoff/keys", "", `{"name":"iPhone","noteId":"`+note+`"}`)
		if w.Code != 201 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		var data map[string]string
		if err = json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		return data["token"], data["id"]
	}
	token, id := create("")
	if w := request(mux, "PATCH", "/api/handoff/keys", "", `{"id":"`+id+`","name":"iPhone 剪贴板"}`); w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	if w := request(mux, "PATCH", "/api/handoff/keys", "", `{"id":"missing","name":"other"}`); w.Code != 404 {
		t.Fatalf("rename missing key: %d", w.Code)
	}
	for _, path := range []string{"/api/notes", "/api/handoff/keys"} {
		if w := request(protected, "GET", path, token, ""); w.Code != 401 {
			t.Fatalf("token escaped scope: %s %d", path, w.Code)
		}
	}
	if w := request(protected, "GET", "/api/handoff/notes", "wrong", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := request(protected, "GET", "/api/handoff/notes", token, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "original") || strings.Contains(w.Body.String(), "hash") {
		t.Fatal(w.Body.String())
	}
	var listing struct {
		Notes   []map[string]string `json:"notes"`
		Choices []string            `json:"choices"`
		NoteIDs map[string]string   `json:"noteIds"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Choices) != 2 || listing.Choices[0] == listing.Choices[1] || listing.NoteIDs[listing.Choices[0]] == "" {
		t.Fatal(w.Body.String())
	}
	body := `{"noteId":"a","text":"from clipboard","requestId":"unique"}`
	for i := 0; i < 2; i++ {
		w = request(protected, "POST", "/api/handoff/append", token, body)
		if w.Code != 200 {
			t.Fatalf("append %d: %s", w.Code, w.Body.String())
		}
	}
	items, _ := store.List()
	for _, n := range items {
		if n.ID == "a" && strings.Count(n.Body, "from clipboard") != 1 {
			t.Fatal(n.Body)
		}
	}
	if w = request(protected, "POST", "/api/handoff/append", token, `{"noteId":"a","text":"changed","requestId":"unique"}`); w.Code != 409 {
		t.Fatalf("conflict %d", w.Code)
	}
	for _, body := range []string{`{"noteId":"a","text":" "}`, `{"noteId":"a","text":"x","format":"html"}`, `{"noteId":"a","text":"x","images":[]}`} {
		if w = request(protected, "POST", "/api/handoff/append", token, body); w.Code != 400 {
			t.Fatalf("invalid %d %s", w.Code, w.Body.String())
		}
	}
	if w = request(mux, "POST", "/api/handoff/keys", "", `{"name":"obsolete","noteId":"a"}`); w.Code != 400 {
		t.Fatal("accepted new single-note grant")
	}
	w = request(mux, "POST", "/api/handoff/keys", "", `{"name":"selected","noteIds":["a"]}`)
	if w.Code != 201 {
		t.Fatalf("create selected grant: %d %s", w.Code, w.Body.String())
	}
	var selectedKey map[string]string
	if err = json.Unmarshal(w.Body.Bytes(), &selectedKey); err != nil {
		t.Fatal(err)
	}
	w = request(protected, "GET", "/api/handoff/notes", selectedKey["token"], "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"a"`) || strings.Contains(w.Body.String(), `"id":"b"`) {
		t.Fatalf("selected notes scope: %s", w.Body.String())
	}
	if w = request(protected, "POST", "/api/handoff/append", selectedKey["token"], `{"noteId":"b","text":"no","requestId":"selected-b"}`); w.Code != 403 {
		t.Fatalf("selected key escaped scope: %d", w.Code)
	}
	if w = request(protected, "POST", "/api/handoff/append", selectedKey["token"], `{"noteId":"a","text":"selected grant","requestId":"selected-a"}`); w.Code != 200 {
		t.Fatalf("selected key append: %d %s", w.Code, w.Body.String())
	}
	if w = request(protected, "POST", "/api/handoff/append", token, `{"noteId":"outside","text":"no"}`); w.Code != 403 {
		t.Fatal("escaped handoff folder")
	}
	limited, _ := create("")
	// Legacy single-note keys keep their narrower grant, intersected with membership.
	if err = settings.update(func(next *savedSettings) error {
		for i := range next.HandoffKeys {
			if next.HandoffKeys[i].Hash == handoffDigest(limited) {
				next.HandoffKeys[i].NoteID = "a"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if w = request(protected, "POST", "/api/handoff/append", limited, `{"noteId":"b","text":"no"}`); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w = request(protected, "POST", "/api/handoff/append", limited, `{"text":"bound"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = request(protected, "POST", "/api/handoff/append", token, `{"noteId":"missing","text":"no"}`); w.Code != 404 {
		t.Fatal(w.Code)
	}
	// Moving out revokes access immediately, including idempotent retries.
	if _, err = store.UpdateFolders(notes.FolderAction{Action: "move", ID: "", NoteIDs: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	for _, grant := range []string{token, limited} {
		if w = request(protected, "POST", "/api/handoff/append", grant, body); w.Code != 403 {
			t.Fatalf("moved-out note accepted: %d", w.Code)
		}
	}
	w = request(protected, "GET", "/api/handoff/notes", token, "")
	if err = json.Unmarshal(w.Body.Bytes(), &listing); err != nil || len(listing.Notes) != 1 || listing.Notes[0]["id"] != "b" {
		t.Fatal(w.Body.String())
	}
	// Moving back restores the grant; keys also cover later additions.
	if _, err = store.UpdateFolders(notes.FolderAction{Action: "move", ID: notes.HandoffFolderID, NoteIDs: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpdateFolders(notes.FolderAction{Action: "move", ID: notes.HandoffFolderID, NoteIDs: []string{"outside"}}); err != nil {
		t.Fatal(err)
	}
	if w = request(protected, "POST", "/api/handoff/append", token, `{"noteId":"outside","text":"now allowed"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err = store.MoveToTrash("outside"); err != nil {
		t.Fatal(err)
	}
	if w = request(protected, "POST", "/api/handoff/append", token, `{"noteId":"outside","text":"no"}`); w.Code != 404 {
		t.Fatalf("accepted trashed note: %d", w.Code)
	}
	w = request(protected, "GET", "/api/handoff/notes", token, "")
	if err = json.Unmarshal(w.Body.Bytes(), &listing); err != nil || len(listing.Notes) != 2 {
		t.Fatal(w.Body.String())
	}
	w = request(mux, "GET", "/api/handoff/keys", "", "")
	if strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), "hash") {
		t.Fatal("secret exposed")
	}

	second, err := state.addLibrary(filepath.Join(t.TempDir(), "second"), "Second")
	if err != nil {
		t.Fatal(err)
	}
	_, secondStore, err := state.active()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = secondStore.Upsert(notes.Note{ID: "a", Title: "Other library", Body: "untouched"}); err != nil {
		t.Fatal(err)
	}
	w = request(protected, "POST", "/api/handoff/append", token, `{"noteId":"a","text":"original library only"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	secondItems, _ := secondStore.List()
	if secondItems[0].Body != "untouched" {
		t.Fatal("key followed active library")
	}
	active, _, _ := state.active()
	if active.Manifest.ID != second.ID {
		t.Fatal("handoff changed active library")
	}
	if err = state.activate(layout.Manifest.ID); err != nil {
		t.Fatal(err)
	}
	persisted, err := openSettingsFile(settings.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.snapshot().HandoffKeys) != 3 || persisted.snapshot().HandoffKeys[0].Hash == token {
		t.Fatal("key persistence")
	}
	if w = request(mux, "DELETE", "/api/handoff/keys?id="+id, "", ""); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	if w = request(protected, "GET", "/api/handoff/notes", token, ""); w.Code != 401 {
		t.Fatal("revoked key accepted")
	}
}
