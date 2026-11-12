package notes

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf8"
)

const HandoffFolderID = "handoff"

// Folders organize entries without changing their stable storage paths.
type Folder struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	NoteIDs []string `json:"noteIds"`
}

type FolderAction struct {
	Action  string   `json:"action"`
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	NoteIDs []string `json:"noteIds"`
}

func (s *Store) readFolders() ([]Folder, error) {
	folders := []Folder{}
	data, err := os.ReadFile(s.foldersPath)
	if errors.Is(err, os.ErrNotExist) {
		return []Folder{{ID: HandoffFolderID, Name: "接力", NoteIDs: []string{}}}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &folders); err != nil {
		return nil, err
	}
	if folders == nil {
		folders = []Folder{}
	}
	if !slices.ContainsFunc(folders, func(f Folder) bool { return f.ID == HandoffFolderID }) {
		folders = append(folders, Folder{ID: HandoffFolderID, Name: "接力", NoteIDs: []string{}})
	}
	return folders, nil
}

func (s *Store) Folders() ([]Folder, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readFolders()
}

func (s *Store) UpdateFolders(input FolderAction) ([]Folder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	folders, err := s.readFolders()
	if err != nil {
		return nil, err
	}
	index := slices.IndexFunc(folders, func(f Folder) bool { return f.ID == input.ID })
	if input.ID == HandoffFolderID && input.Action != "move" {
		return nil, fmt.Errorf("接力是系统文件夹，不能创建、重命名或删除")
	}
	switch input.Action {
	case "create", "rename":
		name := strings.TrimSpace(input.Name)
		if name == "" || utf8.RuneCountInString(name) > 120 {
			return nil, fmt.Errorf("文件夹名称需为 1–120 个字符")
		}
		if input.ID == "" || len(input.ID) > 120 {
			return nil, fmt.Errorf("文件夹标识无效")
		}
		if input.Action == "create" {
			if index >= 0 {
				return nil, fmt.Errorf("文件夹已存在")
			}
			folders = append(folders, Folder{ID: input.ID, Name: name, NoteIDs: []string{}})
		} else {
			if index < 0 {
				return nil, fmt.Errorf("文件夹不存在")
			}
			folders[index].Name = name
		}
	case "delete":
		if index < 0 {
			return nil, fmt.Errorf("文件夹不存在")
		}
		folders = slices.Delete(folders, index, index+1)
	case "move":
		if input.ID != "" && index < 0 {
			return nil, fmt.Errorf("文件夹不存在")
		}
		ids := map[string]bool{}
		for _, id := range input.NoteIDs {
			if id != "" {
				ids[id] = true
			}
		}
		for i := range folders {
			folders[i].NoteIDs = slices.DeleteFunc(folders[i].NoteIDs, func(id string) bool { return ids[id] })
		}
		if index >= 0 {
			for _, id := range input.NoteIDs {
				if ids[id] {
					folders[index].NoteIDs = append(folders[index].NoteIDs, id)
					delete(ids, id)
				}
			}
		}
	default:
		return nil, fmt.Errorf("文件夹操作无效")
	}
	if err := atomicJSON(s.foldersPath, folders); err != nil {
		return nil, err
	}
	return folders, nil
}
