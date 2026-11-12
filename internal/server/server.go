package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"echodiary/internal/library"
	"echodiary/internal/notes"
	"echodiary/web"
)

const maxImageSize = 20 << 20

func imageFormat(data []byte) (string, string) {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return ".jpg", "image/jpeg"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return ".png", "image/png"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return ".gif", "image/gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return ".webp", "image/webp"
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		switch string(data[8:12]) {
		case "avif", "avis":
			return ".avif", "image/avif"
		case "heic", "heix", "hevc", "hevx", "mif1", "msf1":
			return ".heic", "image/heic"
		}
	case bytes.HasPrefix(data, []byte("BM")):
		return ".bmp", "image/bmp"
	}
	return "", ""
}

func imageUploadHandler(imageDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxImageSize+(1<<20))
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				http.Error(w, "image must be smaller than 20 MB", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "invalid image upload", http.StatusBadRequest)
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		file, _, err := r.FormFile("image")
		if err != nil {
			http.Error(w, "invalid image upload", http.StatusBadRequest)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxImageSize+1))
		if err != nil {
			http.Error(w, "could not read image", http.StatusBadRequest)
			return
		}
		if len(data) == 0 || len(data) > maxImageSize {
			http.Error(w, "image must be smaller than 20 MB", http.StatusRequestEntityTooLarge)
			return
		}
		extension, contentType := imageFormat(data)
		if extension == "" {
			http.Error(w, "unsupported image format", http.StatusUnsupportedMediaType)
			return
		}
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			http.Error(w, "could not create image id", http.StatusInternalServerError)
			return
		}
		name := "img-" + hex.EncodeToString(id) + extension
		if err := os.MkdirAll(imageDir, 0o755); err != nil {
			http.Error(w, "could not create image storage", http.StatusInternalServerError)
			return
		}
		path := filepath.Join(imageDir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			http.Error(w, "could not save image", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "/media/" + name, "contentType": contentType})
	}
}

func libraryInfoHandler(layout library.Layout) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         layout.Manifest.ID,
			"name":       layout.Manifest.Name,
			"dataFolder": ".echodiary",
			"version":    layout.Manifest.Version,
		})
	}
}

func validImageName(name string) bool {
	if name != filepath.Base(name) || strings.ContainsAny(name, `/\\`) || !strings.HasPrefix(name, "img-") {
		return false
	}
	extension := filepath.Ext(name)
	if extension != ".jpg" && extension != ".png" && extension != ".gif" && extension != ".webp" && extension != ".avif" && extension != ".heic" && extension != ".bmp" {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(name, "img-"), extension)
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 16
}

func kitDirectory(explicit string) (string, error) {
	candidates := []string{}
	if explicit != "" {
		candidates = append(candidates, explicit)
	} else {
		cwd, _ := os.Getwd()
		executable, _ := os.Executable()
		for _, start := range []string{cwd, filepath.Dir(executable)} {
			for dir, count := start, 0; count < 4; count++ {
				candidates = append(candidates, filepath.Join(dir, "EchoKit"), filepath.Join(dir, "..", "EchoKit"))
				dir = filepath.Dir(dir)
			}
		}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(filepath.Join(candidate, "components", "button", "index.js")); err == nil && !info.IsDir() {
			return filepath.Abs(candidate)
		}
	}
	return "", fmt.Errorf("EchoKit not found; provide -kit /path/to/EchoKit or set ECHOKIT_DIR")
}

func Run() error {
	port := flag.Int("port", 3001, "HTTP port")
	host := flag.String("host", "0.0.0.0", "Listen address (127.0.0.1 for local access only)")
	kit := flag.String("kit", os.Getenv("ECHOKIT_DIR"), "EchoKit directory")
	libraryPath := flag.String("library", os.Getenv("ECHODIARY_LIBRARY"), "Diary library folder")
	flag.Parse()
	kitPath, err := kitDirectory(*kit)
	if err != nil {
		return err
	}
	assets, err := fs.Sub(web.Files, "static")
	if err != nil {
		return err
	}
	settingsPath, err := defaultSettingsPath()
	if err != nil {
		return err
	}
	settings, err := openSettingsFile(settingsPath)
	if err != nil {
		return err
	}
	explicitLibrary := strings.TrimSpace(*libraryPath) != ""
	if strings.TrimSpace(*libraryPath) == "" {
		saved := settings.snapshot()
		for _, entry := range saved.Libraries {
			if entry.ID == saved.ActiveLibrary {
				*libraryPath = entry.Path
				break
			}
		}
		if strings.TrimSpace(*libraryPath) == "" {
			*libraryPath, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve default diary library folder: %w", err)
			}
		}
	}
	libraryLayout, err := library.Open(*libraryPath)
	if err != nil {
		return err
	}
	legacyDataDir := os.Getenv("ECHODIARY_DATA")
	if legacyDataDir == "" {
		cwd, _ := os.Getwd()
		legacyDataDir = filepath.Join(cwd, ".echodiary-data")
	}
	legacyDataDir, err = filepath.Abs(legacyDataDir)
	if err != nil {
		return fmt.Errorf("resolve legacy diary data folder: %w", err)
	}
	migrationMarker := filepath.Join(filepath.Dir(legacyDataDir), ".echodiary-library-migration-v1")
	copied, migrationErr := library.MigrateLegacyDataOnce(legacyDataDir, libraryLayout.DataDir, migrationMarker)
	if migrationErr != nil {
		return migrationErr
	}
	if copied {
		log.Printf("Copied legacy diary data into library %s; original data retained", libraryLayout.Root)
	} else if _, sourceErr := os.Stat(filepath.Join(legacyDataDir, "notes.sqlite")); sourceErr == nil {
		if _, destinationErr := os.Stat(filepath.Join(libraryLayout.DataDir, "notes.sqlite")); destinationErr == nil {
			log.Printf("Existing library database was kept; legacy data remains at %s", legacyDataDir)
		} else if _, markerErr := os.Stat(migrationMarker); markerErr == nil {
			log.Printf("Legacy diary data was already copied once; original remains at %s", legacyDataDir)
		}
	} else if !errors.Is(sourceErr, os.ErrNotExist) {
		return fmt.Errorf("check legacy diary database: %w", sourceErr)
	}
	noteStore, err := notes.Open(libraryLayout.DataDir)
	if err != nil {
		return fmt.Errorf("open EchoDiary library data: %w", err)
	}
	runtimeState, err := newDiaryRuntime(settings, &libraryHandle{layout: libraryLayout, store: noteStore}, explicitLibrary)
	if err != nil {
		_ = noteStore.Close()
		return err
	}
	defer runtimeState.close()
	authSessions := newSessions(settings)
	realtime := newRealtimeHub()
	mux := http.NewServeMux()
	mux.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		data, err := fs.ReadFile(assets, "sw.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		_, _ = w.Write([]byte(strings.ReplaceAll(string(data), "{{ASSET_VERSION}}", "20261112-system-theme")))
	})
	mux.HandleFunc("/api/auth/status", authStatusHandler(settings, runtimeState, authSessions))
	mux.HandleFunc("/api/auth/setup", setupHandler(settings, runtimeState, authSessions))
	mux.HandleFunc("/api/auth/login", loginHandler(settings, authSessions))
	mux.HandleFunc("/api/auth/logout", logoutHandler(authSessions))
	mux.HandleFunc("/api/library", currentLibraryInfoHandler(runtimeState))
	mux.HandleFunc("/api/events", realtimeHandler(runtimeState, realtime))
	mux.HandleFunc("/api/revision", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		layout, _, err := runtimeState.active()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"libraryId": layout.Manifest.ID,
			"revision":  realtime.revision(layout.Manifest.ID),
		})
	})
	mux.HandleFunc("/api/folders", foldersHandler(runtimeState, realtime))
	mux.HandleFunc("/api/handoff/keys", handoffKeysHandler(settings, runtimeState))
	mux.HandleFunc("/api/handoff/notes", handoffHandler(settings, runtimeState))
	mux.HandleFunc("/api/handoff/append", handoffHandler(settings, runtimeState, realtime))
	mux.HandleFunc("/api/libraries", librariesHandler(runtimeState))
	mux.HandleFunc("/api/libraries/", libraryActionHandler(runtimeState, realtime))
	mux.HandleFunc("/api/imports/kehua", kehuaImportHandler(runtimeState, realtime))
	mux.HandleFunc("/api/images", func(w http.ResponseWriter, r *http.Request) {
		layout, _, err := runtimeState.active()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		imageUploadHandler(layout.ImageDir)(w, r)
		runtimeState.invalidateStats(layout.Manifest.ID)
	})
	mux.HandleFunc("/api/images/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/images/")
		if !validImageName(name) {
			http.NotFound(w, r)
			return
		}
		layout, noteStore, err := runtimeState.active()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		imageURL := "/media/" + name
		used, err := noteStore.UsesImage(imageURL)
		if err != nil {
			http.Error(w, "could not check image references", http.StatusInternalServerError)
			return
		}
		if used {
			http.Error(w, "image is still attached to a note", http.StatusConflict)
			return
		}
		if err := os.Remove(filepath.Join(layout.ImageDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			http.Error(w, "could not remove image", http.StatusInternalServerError)
			return
		}
		runtimeState.invalidateStats(layout.Manifest.ID)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/media/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/media/")
		if !validImageName(name) {
			http.NotFound(w, r)
			return
		}
		_, store, err := runtimeState.active()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		contentTypes := map[string]string{
			".jpg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp",
			".avif": "image/avif", ".heic": "image/heic", ".bmp": "image/bmp",
		}
		w.Header().Set("Content-Type", contentTypes[filepath.Ext(name)])
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		imagePath, err := store.ImagePath(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, imagePath)
	})
	mux.HandleFunc("/api/notes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		layout, noteStore, err := runtimeState.active()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		switch r.Method {
		case http.MethodGet:
			items, queryErr := noteStore.List()
			if queryErr != nil {
				http.Error(w, queryErr.Error(), 500)
				return
			}
			bootstrap, stateErr := noteStore.BootstrapPending()
			if stateErr != nil {
				http.Error(w, stateErr.Error(), 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"notes": items, "bootstrap": bootstrap})
		case http.MethodPost:
			var note notes.Note
			if json.NewDecoder(r.Body).Decode(&note) != nil {
				http.Error(w, "invalid note", 400)
				return
			}
			saved, saveErr := noteStore.Upsert(note)
			if saveErr != nil {
				http.Error(w, saveErr.Error(), 500)
				return
			}
			runtimeState.invalidateActiveStats()
			realtime.publish(layout.Manifest.ID, "notes")
			_ = json.NewEncoder(w).Encode(saved)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/notes/initialize", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		layout, noteStore, err := runtimeState.active()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		var seed []notes.Note
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&seed); err != nil {
			http.Error(w, "invalid initialization payload", http.StatusBadRequest)
			return
		}
		if err := noteStore.Initialize(seed); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		runtimeState.invalidateActiveStats()
		realtime.publish(layout.Manifest.ID, "notes")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/notes/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		id := strings.TrimPrefix(r.URL.Path, "/api/notes/")
		if id == "" {
			http.NotFound(w, r)
			return
		}
		layout, noteStore, err := runtimeState.active()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		switch r.Method {
		case http.MethodPut, http.MethodPatch:
			var note notes.Note
			if json.NewDecoder(r.Body).Decode(&note) != nil {
				http.Error(w, "invalid note", 400)
				return
			}
			note.ID = id
			saved, saveErr := noteStore.Upsert(note)
			if saveErr != nil {
				http.Error(w, saveErr.Error(), 500)
				return
			}
			runtimeState.invalidateActiveStats()
			realtime.publish(layout.Manifest.ID, "notes")
			_ = json.NewEncoder(w).Encode(saved)
		case http.MethodDelete:
			if deleteErr := noteStore.MoveToTrash(id); deleteErr != nil {
				http.Error(w, deleteErr.Error(), 500)
				return
			}
			runtimeState.invalidateActiveStats()
			realtime.publish(layout.Manifest.ID, "notes")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/trash", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		layout, noteStore, err := runtimeState.active()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		switch r.Method {
		case http.MethodGet:
			items, listErr := noteStore.ListTrash()
			if listErr != nil {
				http.Error(w, listErr.Error(), http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"notes": items})
		case http.MethodPost:
			var payload struct {
				ID     string `json:"id"`
				Action string `json:"action"`
			}
			if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.ID == "" {
				http.Error(w, "invalid trash request", http.StatusBadRequest)
				return
			}
			if payload.Action == "restore" {
				if err = noteStore.Restore(payload.ID); err != nil {
					http.Error(w, err.Error(), http.StatusNotFound)
					return
				}
			} else if payload.Action == "delete" {
				if _, err = noteStore.PermanentlyDelete(payload.ID, filepath.Join(layout.DataDir, "trash")); err != nil {
					http.Error(w, err.Error(), http.StatusNotFound)
					return
				}
			} else {
				http.Error(w, "invalid trash action", http.StatusBadRequest)
				return
			}
			runtimeState.invalidateActiveStats()
			realtime.publish(layout.Manifest.ID, "notes")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	kitFiles := http.StripPrefix("/kit/", http.FileServer(http.Dir(kitPath)))
	mux.HandleFunc("/kit/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/kit/")
		allowed := false
		for _, prefix := range []string{"css/", "tokens/", "components/", "SFSymbols/"} {
			allowed = allowed || strings.HasPrefix(path, prefix)
		}
		if !allowed {
			http.NotFound(w, r)
			return
		}
		kitFiles.ServeHTTP(w, r)
	})
	staticFiles := http.FileServer(http.FS(assets))
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		r.URL.Path = "/assets/favicon.ico"
		staticFiles.ServeHTTP(w, r)
	})
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest.webmanifest" {
			w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
		}
		// HTML, CSS, and JS must refresh after rebuilding the embedded frontend.
		if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, ".html") || strings.HasSuffix(r.URL.Path, ".css") || strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".ico") || strings.HasSuffix(r.URL.Path, ".webmanifest") || strings.HasPrefix(r.URL.Path, "/assets/favicon-") || r.URL.Path == "/assets/apple-touch-icon.png" {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}
		staticFiles.ServeHTTP(w, r)
	}))
	var server *http.Server
	mux.HandleFunc("/api/settings/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		go func() {
			shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownContext)
		}()
	})
	protectedMux := authRequired(settings, authSessions, mux)
	server = &http.Server{
		Addr: net.JoinHostPort(*host, fmt.Sprint(*port)),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "same-origin")
			w.Header().Set("Cache-Control", "no-cache")
			protectedMux.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("EchoDiary listening on http://127.0.0.1:%d", *port)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
