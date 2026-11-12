package library

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const dataDirName = ".echodiary"

type Manifest struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
}

type Layout struct {
	Root       string
	DataDir    string
	ImageDir   string // Upload/import staging; saved attachments live in EntriesDir.
	EntriesDir string
	Manifest   Manifest
}

func Open(root string) (Layout, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return Layout{}, fmt.Errorf("library path is required; use -library or ECHODIARY_LIBRARY")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Layout{}, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return Layout{}, fmt.Errorf("open library folder: %w", err)
	}
	if !info.IsDir() {
		return Layout{}, fmt.Errorf("library path is not a directory: %s", absolute)
	}
	dataDir := filepath.Join(absolute, dataDirName)
	if err := os.MkdirAll(filepath.Join(dataDir, "images"), 0o755); err != nil {
		return Layout{}, fmt.Errorf("create library data folder: %w", err)
	}
	manifestPath := filepath.Join(dataDir, "library.json")
	manifest := Manifest{}
	data, err := os.ReadFile(manifestPath)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &manifest); err != nil {
			return Layout{}, fmt.Errorf("invalid library manifest %s: %w", manifestPath, err)
		}
		if manifest.ID == "" || (manifest.Version != 1 && manifest.Version != 2) {
			return Layout{}, fmt.Errorf("unsupported or incomplete library manifest: %s", manifestPath)
		}
	case errors.Is(err, os.ErrNotExist):
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return Layout{}, fmt.Errorf("create library id: %w", err)
		}
		manifest = Manifest{ID: hex.EncodeToString(id), Name: filepath.Base(absolute), Version: 2, CreatedAt: time.Now().UTC()}
		encoded, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return Layout{}, err
		}
		if err := os.WriteFile(manifestPath, append(encoded, '\n'), 0o644); err != nil {
			return Layout{}, fmt.Errorf("write library manifest: %w", err)
		}
	default:
		return Layout{}, fmt.Errorf("read library manifest: %w", err)
	}
	return Layout{Root: absolute, DataDir: dataDir, ImageDir: filepath.Join(dataDir, "images"), EntriesDir: filepath.Join(absolute, "entries"), Manifest: manifest}, nil
}

// Load reads an existing library without creating or modifying its data folder.
func Load(root string) (Layout, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return Layout{}, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return Layout{}, fmt.Errorf("open library folder: %w", err)
	}
	if !info.IsDir() {
		return Layout{}, fmt.Errorf("library path is not a directory: %s", absolute)
	}
	dataDir := filepath.Join(absolute, dataDirName)
	if info, err := os.Stat(dataDir); err != nil {
		return Layout{}, fmt.Errorf("open EchoDiary library data folder: %w", err)
	} else if !info.IsDir() {
		return Layout{}, fmt.Errorf("EchoDiary library data path is not a directory: %s", dataDir)
	}
	manifestPath := filepath.Join(dataDir, "library.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return Layout{}, fmt.Errorf("read library manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Layout{}, fmt.Errorf("invalid library manifest %s: %w", manifestPath, err)
	}
	if manifest.ID == "" || (manifest.Version != 1 && manifest.Version != 2) {
		return Layout{}, fmt.Errorf("unsupported or incomplete library manifest: %s", manifestPath)
	}
	return Layout{Root: absolute, DataDir: dataDir, ImageDir: filepath.Join(dataDir, "images"), EntriesDir: filepath.Join(absolute, "entries"), Manifest: manifest}, nil
}

// Rename updates only the portable library manifest; notes and images are untouched.
func Rename(root, name string) (Layout, error) {
	layout, err := Load(root)
	if err != nil {
		return Layout{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Layout{}, fmt.Errorf("library name is required")
	}
	layout.Manifest.Name = name
	encoded, err := json.MarshalIndent(layout.Manifest, "", "  ")
	if err != nil {
		return Layout{}, err
	}
	manifestPath := filepath.Join(layout.DataDir, "library.json")
	temporary, err := os.CreateTemp(layout.DataDir, ".library-*.json")
	if err != nil {
		return Layout{}, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return Layout{}, err
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		_ = temporary.Close()
		return Layout{}, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return Layout{}, err
	}
	if err := temporary.Close(); err != nil {
		return Layout{}, err
	}
	if err := os.Rename(temporaryPath, manifestPath); err != nil {
		return Layout{}, err
	}
	return layout, nil
}

// CopyLegacyData copies the old app-local data only into a pristine library.
// The source is never modified or removed.
func CopyLegacyData(source, destination string) (bool, error) {
	// A missing index does not make a file-backed library pristine.
	if _, err := os.Stat(filepath.Join(destination, "storage.json")); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	if strings.TrimSpace(source) == "" || filepath.Clean(source) == filepath.Clean(destination) {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(destination, "notes.sqlite")); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	sourceDB := filepath.Join(source, "notes.sqlite")
	if _, err := os.Stat(sourceDB); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	stage, err := os.MkdirTemp(destination, ".migration-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(stage)
	stageDB := filepath.Join(stage, "notes.sqlite")
	db, err := sql.Open("sqlite", sourceDB)
	if err != nil {
		return false, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=10000`); err != nil {
		_ = db.Close()
		return false, fmt.Errorf("prepare legacy diary database snapshot: %w", err)
	}
	quotedPath := strings.ReplaceAll(filepath.ToSlash(stageDB), "'", "''")
	_, snapshotErr := db.Exec(`VACUUM INTO '` + quotedPath + `'`)
	closeErr := db.Close()
	if snapshotErr != nil {
		return false, fmt.Errorf("snapshot legacy diary database: %w", snapshotErr)
	}
	if closeErr != nil {
		return false, closeErr
	}
	stageImages := filepath.Join(stage, "images")
	if err := os.MkdirAll(stageImages, 0o755); err != nil {
		return false, err
	}
	if err := copyDirectory(filepath.Join(source, "images"), stageImages); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("copy legacy diary images: %w", err)
	}
	if err := copyDirectory(stageImages, filepath.Join(destination, "images")); err != nil {
		return false, fmt.Errorf("install legacy diary images: %w", err)
	}
	if err := os.Rename(stageDB, filepath.Join(destination, "notes.sqlite")); err != nil {
		return false, fmt.Errorf("install legacy diary database: %w", err)
	}
	return true, nil
}

// MigrateLegacyDataOnce copies legacy data only when the destination has no
// database. The marker is written after a successful copy; an existing
// library is never treated as a completed migration so a later empty library
// can still receive the untouched legacy data.
func MigrateLegacyDataOnce(source, destination, marker string) (bool, error) {
	// A missing index does not make a file-backed library pristine.
	if _, err := os.Stat(filepath.Join(destination, "storage.json")); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	if _, err := os.Stat(marker); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("check legacy migration marker: %w", err)
	}
	if _, err := os.Stat(filepath.Join(source, "notes.sqlite")); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("check legacy diary database: %w", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "notes.sqlite")); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("check destination diary database: %w", err)
	}

	copied, err := CopyLegacyData(source, destination)
	if err != nil || !copied {
		return copied, err
	}
	if err := writeMigrationMarker(marker); err != nil {
		return true, fmt.Errorf("record completed legacy migration: %w", err)
	}
	return true, nil
}

func writeMigrationMarker(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".echodiary-migration-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := io.WriteString(file, "copied\n"); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func copyDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			_ = input.Close()
			if errors.Is(err, os.ErrExist) {
				equal, compareErr := filesEqual(path, target)
				if compareErr != nil {
					return compareErr
				}
				if !equal {
					return fmt.Errorf("legacy image conflicts with existing library image: %s", target)
				}
				return nil
			}
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		return closeErr
	})
}

func filesEqual(leftPath, rightPath string) (bool, error) {
	left, err := os.Open(leftPath)
	if err != nil {
		return false, err
	}
	defer left.Close()
	right, err := os.Open(rightPath)
	if err != nil {
		return false, err
	}
	defer right.Close()
	leftHash, rightHash := sha256.New(), sha256.New()
	if _, err := io.Copy(leftHash, left); err != nil {
		return false, err
	}
	if _, err := io.Copy(rightHash, right); err != nil {
		return false, err
	}
	return bytes.Equal(leftHash.Sum(nil), rightHash.Sum(nil)), nil
}
