// migrate-library converts an existing library without starting the HTTP server.
// Stop older EchoDiary processes and back up the complete library before use.
package main

import (
	"echodiary/internal/library"
	"echodiary/internal/notes"
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := flag.String("library", "", "Existing EchoDiary library root")
	flag.Parse()
	if strings.TrimSpace(*root) == "" {
		log.Fatal("-library is required")
	}
	layout, err := library.Load(*root)
	if err != nil {
		log.Fatal(err)
	}
	store, err := notes.Open(layout.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	items, err := store.List()
	if err != nil {
		log.Fatal(err)
	}
	trash, err := store.ListTrash()
	if err != nil {
		log.Fatal(err)
	}
	imageCount := 0
	check := func(n notes.Note, dir string) {
		for _, url := range n.Images {
			if strings.HasPrefix(url, "/media/") {
				name := strings.TrimPrefix(url, "/media/")
				if filepath.Base(name) != name {
					log.Fatal("invalid image reference")
				}
				folder := filepath.Join(dir, n.ID)
				if dir == layout.EntriesDir {
					var err error
					folder, err = store.EntryDirectory(n.ID)
					if err != nil {
						log.Fatal(err)
					}
				}
				if _, err := os.Stat(filepath.Join(folder, "images", name)); err != nil {
					log.Fatalf("attachment verification: %v", err)
				}
				imageCount++
			}
		}
	}
	for _, n := range items {
		check(n, layout.EntriesDir)
	}
	for _, n := range trash {
		check(n.Note, filepath.Join(layout.DataDir, "recycle-bin"))
	}
	pending, err := store.BootstrapPending()
	if err != nil {
		log.Fatal(err)
	}
	if err = json.NewEncoder(os.Stdout).Encode(map[string]any{"libraryId": layout.Manifest.ID, "entries": len(items), "trash": len(trash), "imageReferencesVerified": imageCount, "bootstrapPending": pending}); err != nil {
		log.Fatal(err)
	}
}
