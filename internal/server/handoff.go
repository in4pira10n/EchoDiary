package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"echodiary/internal/notes"
	"github.com/google/uuid"
)

type handoffKey struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	LibraryID string   `json:"libraryId"`
	NoteID    string   `json:"noteId,omitempty"`
	NoteIDs   []string `json:"noteIds,omitempty"`
	Hash      string   `json:"hash"`
	CreatedAt string   `json:"createdAt"`
}

func handoffDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func handoffKeysHandler(settings *settingsFile, state *diaryRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		layout, store, err := state.active()
		if err != nil {
			writeJSON(w, 503, map[string]string{"error": "资源库不可用"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			keys := []map[string]any{}
			for _, key := range settings.snapshot().HandoffKeys {
				if key.LibraryID == layout.Manifest.ID {
					keys = append(keys, map[string]any{"id": key.ID, "name": key.Name, "noteId": key.NoteID, "noteIds": key.NoteIDs, "createdAt": key.CreatedAt})
				}
			}
			writeJSON(w, 200, map[string]any{"keys": keys, "libraryName": layout.Manifest.Name, "libraryId": layout.Manifest.ID})
		case http.MethodPost:
			var input struct {
				Name    string   `json:"name"`
				NoteID  string   `json:"noteId"`
				NoteIDs []string `json:"noteIds"`
			}
			if decodeJSON(w, r, &input, 4096) != nil || len(strings.TrimSpace(input.Name)) == 0 || len([]rune(input.Name)) > 60 {
				writeJSON(w, 400, map[string]string{"error": "请输入密钥名称（最多 60 个字符）"})
				return
			}
			if input.NoteID != "" && len(input.NoteIDs) > 0 {
				writeJSON(w, 400, map[string]string{"error": "请使用多篇日记授权列表"})
				return
			}
			if input.NoteID != "" {
				writeJSON(w, 400, map[string]string{"error": "请使用 noteIds 多篇日记授权列表"})
				return
			}
			if len(input.NoteIDs) > 0 {
				folders, folderErr := store.Folders()
				items, listErr := store.List()
				if folderErr != nil || listErr != nil {
					writeJSON(w, 503, map[string]string{"error": "无法读取接力笔记"})
					return
				}
				members := map[string]bool{}
				for _, folder := range folders {
					if folder.ID == notes.HandoffFolderID {
						for _, id := range folder.NoteIDs {
							members[id] = true
						}
					}
				}
				available := map[string]bool{}
				for _, item := range items {
					if members[item.ID] && !item.IsEmpty() {
						available[item.ID] = true
					}
				}
				selected := map[string]bool{}
				for _, id := range input.NoteIDs {
					if !available[id] || selected[id] {
						writeJSON(w, 400, map[string]string{"error": "选择的日记无效或不在接力文件夹中"})
						return
					}
					selected[id] = true
				}
			}
			random := make([]byte, 32)
			if _, err := rand.Read(random); err != nil {
				writeJSON(w, 500, map[string]string{"error": "无法生成密钥"})
				return
			}
			token := "ed_" + base64.RawURLEncoding.EncodeToString(random)
			key := handoffKey{ID: uuid.NewString(), Name: strings.TrimSpace(input.Name), LibraryID: layout.Manifest.ID, NoteID: input.NoteID, NoteIDs: input.NoteIDs, Hash: handoffDigest(token), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
			err = settings.update(func(next *savedSettings) error {
				if len(next.HandoffKeys) >= 64 {
					return errors.New("密钥已达 64 个，请先撤销不用的密钥")
				}
				next.HandoffKeys = append(next.HandoffKeys, key)
				return nil
			})
			if err != nil {
				writeJSON(w, 400, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, 201, map[string]string{"id": key.ID, "token": token})
		case http.MethodPatch:
			var input struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if decodeJSON(w, r, &input, 4096) != nil || input.ID == "" || len(strings.TrimSpace(input.Name)) == 0 || len([]rune(input.Name)) > 60 {
				writeJSON(w, 400, map[string]string{"error": "请输入密钥名称（最多 60 个字符）"})
				return
			}
			found := false
			err := settings.update(func(next *savedSettings) error {
				for i := range next.HandoffKeys {
					if next.HandoffKeys[i].ID == input.ID && next.HandoffKeys[i].LibraryID == layout.Manifest.ID {
						next.HandoffKeys[i].Name = strings.TrimSpace(input.Name)
						found = true
						break
					}
				}
				return nil
			})
			if err != nil {
				writeJSON(w, 500, map[string]string{"error": "无法更新密钥名称"})
				return
			}
			if !found {
				writeJSON(w, 404, map[string]string{"error": "密钥不存在"})
				return
			}
			writeJSON(w, 200, map[string]string{"id": input.ID, "name": strings.TrimSpace(input.Name)})
		case http.MethodDelete:
			id := r.URL.Query().Get("id")
			found := false
			err := settings.update(func(next *savedSettings) error {
				for i, k := range next.HandoffKeys {
					if k.ID == id && k.LibraryID == layout.Manifest.ID {
						next.HandoffKeys = append(next.HandoffKeys[:i], next.HandoffKeys[i+1:]...)
						found = true
						break
					}
				}
				return nil
			})
			if err != nil {
				writeJSON(w, 500, map[string]string{"error": "无法撤销密钥"})
				return
			}
			if !found {
				writeJSON(w, 404, map[string]string{"error": "密钥不存在"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "GET, POST, PATCH, DELETE")
			w.WriteHeader(405)
		}
	}
}

// These two endpoints authenticate their own narrowly scoped bearer capability.
// Session cookies alone cannot authorize them, and bearer keys never authorize
// the ordinary diary APIs or key management.
func handoffHandler(settings *settingsFile, state *diaryRuntime, realtime ...*realtimeHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		fail := func(status int, message string) { writeJSON(w, status, map[string]any{"ok": false, "error": message}) }
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 128 || settings.snapshot().Admin == nil {
			fail(401, "接力密钥无效或已撤销")
			return
		}
		hash := handoffDigest(parts[1])
		var key *handoffKey
		for _, candidate := range settings.snapshot().HandoffKeys {
			if subtle.ConstantTimeCompare([]byte(hash), []byte(candidate.Hash)) == 1 {
				copy := candidate
				key = &copy
			}
		}
		if key == nil {
			fail(401, "接力密钥无效或已撤销")
			return
		}
		handle, err := state.openHandle(key.LibraryID)
		if err != nil {
			fail(503, "密钥绑定的资源库不可用")
			return
		}
		if r.URL.Path == "/api/handoff/notes" {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", "GET")
				fail(405, "请使用 GET")
				return
			}
			items, err := handle.store.List()
			if err != nil {
				fail(503, "无法读取日记")
				return
			}
			folders, err := handle.store.Folders()
			if err != nil {
				fail(503, "无法读取接力文件夹")
				return
			}
			members := map[string]bool{}
			for _, folder := range folders {
				if folder.ID == notes.HandoffFolderID {
					for _, id := range folder.NoteIDs {
						members[id] = true
					}
				}
			}
			choices := []map[string]string{}
			labels := []string{}
			ids := map[string]string{}
			for _, n := range items {
				if !n.IsEmpty() && members[n.ID] && handoffKeyAllows(*key, n.ID) {
					title := n.Title
					if strings.TrimSpace(title) == "" {
						title = "无标题日记"
					}
					label := title + " · " + n.ID
					labels = append(labels, label)
					ids[label] = n.ID
					choices = append(choices, map[string]string{"id": n.ID, "title": title, "createdAt": n.CreatedAt, "updatedAt": n.UpdatedAt})
				}
			}
			writeJSON(w, 200, map[string]any{"ok": true, "libraryName": handle.layout.Manifest.Name, "notes": choices, "choices": labels, "noteIds": ids})
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			fail(405, "请使用 POST")
			return
		}
		var input struct {
			NoteID    string `json:"noteId"`
			Text      string `json:"text"`
			Format    string `json:"format"`
			RequestID string `json:"requestId"`
		}
		if decodeJSON(w, r, &input, 1<<20) != nil {
			fail(400, "请发送 JSON：noteId、text，可选 format 和 requestId（最大 1 MB）")
			return
		}
		if input.NoteID == "" {
			input.NoteID = key.NoteID
			if input.NoteID == "" && len(key.NoteIDs) == 1 {
				input.NoteID = key.NoteIDs[0]
			}
		}
		if input.NoteID == "" || strings.TrimSpace(input.Text) == "" || len(input.RequestID) > 128 {
			fail(400, "日记 ID 和文本不能为空，requestId 最多 128 字节")
			return
		}
		if !handoffKeyAllows(*key, input.NoteID) {
			fail(403, "此密钥无权访问所选日记")
			return
		}
		if input.Format == "" {
			input.Format = "text"
		}
		if input.Format != "text" && input.Format != "markdown" {
			fail(400, "format 仅支持 text 或 markdown")
			return
		}
		saved, duplicate, err := handle.store.AppendHandoff(input.NoteID, input.Text, input.Format, key.ID, input.RequestID)
		if errors.Is(err, notes.ErrHandoffScope) {
			fail(403, "日记不在此资源库的接力文件夹中")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			fail(404, "日记不存在或已在回收站")
			return
		}
		if errors.Is(err, notes.ErrAppendConflict) {
			fail(409, err.Error())
			return
		}
		if err != nil {
			fail(500, "追加失败："+err.Error())
			return
		}
		state.invalidateStats(key.LibraryID)
		if len(realtime) > 0 && !duplicate {
			realtime[0].publish(key.LibraryID, "notes")
		}
		writeJSON(w, 200, map[string]any{"ok": true, "noteId": saved.ID, "title": saved.Title, "updatedAt": saved.UpdatedAt, "duplicate": duplicate})
	}
}

func handoffKeyAllows(key handoffKey, noteID string) bool {
	if key.NoteID != "" {
		return key.NoteID == noteID
	}
	if len(key.NoteIDs) == 0 {
		return true
	}
	for _, id := range key.NoteIDs {
		if id == noteID {
			return true
		}
	}
	return false
}
