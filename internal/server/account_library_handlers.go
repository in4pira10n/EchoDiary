package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	return nil
}

func authStatusHandler(settings *settingsFile, state *diaryRuntime, sessions *sessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		stored := settings.snapshot()
		username, authenticated := sessions.username(sessionToken(r))
		payload := map[string]any{
			"setupRequired": stored.Admin == nil,
			"authenticated": stored.Admin != nil && authenticated,
		}
		if stored.Admin == nil {
			payload["localSetupRequired"] = !isLoopbackClient(r)
			if isLoopbackClient(r) {
				if layout, _, err := state.active(); err == nil {
					payload["suggestedLibraryName"] = layout.Manifest.Name
					payload["suggestedLibraryPath"] = layout.Root
				}
			}
		} else if authenticated {
			payload["username"] = username
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

type setupRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	LibraryName string `json:"libraryName"`
	LibraryPath string `json:"libraryPath"`
}

func setupHandler(settings *settingsFile, state *diaryRuntime, sessions *sessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if settings.snapshot().Admin != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "EchoDiary 已完成初始化，请登录"})
			return
		}
		if !isLoopbackClient(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "首次初始化只能在运行 EchoDiary 的主机上完成"})
			return
		}
		var input setupRequest
		if err := decodeJSON(w, r, &input, 32<<10); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "初始化资料无效"})
			return
		}
		account, err := makeAdminAccount(input.Username, input.Password)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		record, handle, err := state.prepareSetupLibrary(input.LibraryPath, input.LibraryName)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := state.installSetup(account, record, handle); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		token, err := sessions.create(account.Username)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法建立登录会话"})
			return
		}
		setSessionCookie(w, r, token)
		writeJSON(w, http.StatusCreated, map[string]string{"username": account.Username})
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func loginHandler(settings *settingsFile, sessions *sessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var input loginRequest
		if err := decodeJSON(w, r, &input, 16<<10); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "登录信息无效"})
			return
		}
		account := settings.snapshot().Admin
		if !verifyPassword(account, input.Username, input.Password) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "用户名或密码不正确"})
			return
		}
		token, err := sessions.create(account.Username)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法建立登录会话"})
			return
		}
		setSessionCookie(w, r, token)
		writeJSON(w, http.StatusOK, map[string]string{"username": account.Username})
	}
}

func logoutHandler(sessions *sessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		clearSessionCookie(w, r)
		w.WriteHeader(http.StatusNoContent)
	}
}

func currentLibraryInfoHandler(state *diaryRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		layout, _, err := state.active()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": layout.Manifest.ID, "name": layout.Manifest.Name,
			"dataFolder": ".echodiary", "version": layout.Manifest.Version,
		})
	}
}

func librariesHandler(state *diaryRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"libraries": state.listLibraries(r.URL.Query().Get("refresh") == "1")})
		case http.MethodPost:
			var input struct {
				Name string `json:"name"`
				Path string `json:"path"`
			}
			if err := decodeJSON(w, r, &input, 32<<10); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "资源库信息无效"})
				return
			}
			record, err := state.addLibrary(input.Path, input.Name)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			for _, card := range state.listLibraries() {
				if card.ID == record.ID {
					writeJSON(w, http.StatusCreated, card)
					return
				}
			}
			writeJSON(w, http.StatusCreated, record)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func libraryActionHandler(state *diaryRuntime, realtime *realtimeHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/libraries/"), "/")
		parts := strings.Split(rest, "/")
		if len(parts) != 2 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		id, action := parts[0], parts[1]
		switch action {
		case "activate":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if err := state.activate(id); err != nil {
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			layout, _, err := state.active()
			if err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
				return
			}
			realtime.publishAll("library")
			writeJSON(w, http.StatusOK, map[string]string{"id": layout.Manifest.ID, "name": layout.Manifest.Name})
		case "favorite":
			if r.Method != http.MethodPatch {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var input struct {
				Favorite bool `json:"favorite"`
			}
			if err := decodeJSON(w, r, &input, 8<<10); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "收藏设置无效"})
				return
			}
			if err := state.setFavorite(id, input.Favorite); err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"favorite": input.Favorite})
		case "avatar":
			if r.Method != http.MethodPatch {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var input struct {
				Avatar string `json:"avatar"`
			}
			if err := decodeJSON(w, r, &input, 4<<20); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "头像设置无效"})
				return
			}
			if err := state.setAvatar(id, input.Avatar); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"avatar": input.Avatar})
		case "rename":
			if r.Method != http.MethodPatch {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var input struct {
				Name string `json:"name"`
			}
			if err := decodeJSON(w, r, &input, 8<<10); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "名称无效"})
				return
			}
			if err := state.rename(id, input.Name); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"name": strings.TrimSpace(input.Name)})
		case "relocate":
			if r.Method != http.MethodPatch {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var input struct {
				Path string `json:"path"`
			}
			if err := decodeJSON(w, r, &input, 16<<10); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "资源库路径无效"})
				return
			}
			if err := state.relocate(id, input.Path); err != nil {
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"id": id, "path": strings.TrimSpace(input.Path)})
		default:
			http.NotFound(w, r)
		}
	}
}
