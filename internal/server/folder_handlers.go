package server

import (
	"echodiary/internal/notes"
	"net/http"
)

func foldersHandler(state *diaryRuntime, realtime *realtimeHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		layout, store, err := state.active()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		var folders []notes.Folder
		if r.Method == http.MethodGet {
			folders, err = store.Folders()
		} else {
			var input notes.FolderAction
			if err := decodeJSON(w, r, &input, 1<<20); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "文件夹资料无效"})
				return
			}
			folders, err = store.UpdateFolders(input)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if r.Method == http.MethodPost {
			realtime.publish(layout.Manifest.ID, "folders")
		}
		writeJSON(w, http.StatusOK, map[string]any{"folders": folders})
	}
}
