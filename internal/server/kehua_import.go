package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"echodiary/internal/notes"
)

const kehuaTimestampLayout = "2006年01月02日 15:04:05"

var (
	kehuaTimestamp = regexp.MustCompile(`^\d{4}年\d{2}月\d{2}日 \d{2}:\d{2}:\d{2}$`)
	kehuaYearFile  = regexp.MustCompile(`^\d{4}年-动态内容\.txt$`)
	kehuaMedia     = regexp.MustCompile(`\[(图片|视频)[：:]([^\]\r\n]+)\]`)
)

type kehuaEntry struct {
	Timestamp time.Time
	Body      string
	File      string
	Key       string
}

type kehuaReport struct {
	LibraryID string   `json:"libraryId"`
	Total     int      `json:"total"`
	Imported  int      `json:"imported"`
	Skipped   int      `json:"skipped"`
	Empty     int      `json:"empty"`
	Images    int      `json:"images"`
	Warnings  []string `json:"warnings"`
}

func parseKehuaEntries(data []byte, file string) ([]kehuaEntry, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%s 不是 UTF-8 文本，请使用可话原始导出文件", file)
	}
	text := strings.ReplaceAll(strings.TrimPrefix(string(data), "\ufeff"), "\r\n", "\n")
	var entries []kehuaEntry
	var current *kehuaEntry
	var body []string
	flush := func() {
		if current == nil {
			return
		}
		// Remove export separators, preserving paragraph spacing inside the entry.
		current.Body = strings.TrimRight(strings.Join(body, "\n"), "\n")
		hash := sha256.Sum256([]byte(current.Timestamp.Format(kehuaTimestampLayout) + "\n" + current.Body))
		current.Key = fmt.Sprintf("%x", hash)
		entries = append(entries, *current)
	}
	for index, line := range strings.Split(text, "\n") {
		if kehuaTimestamp.MatchString(line) {
			stamp, err := time.ParseInLocation(kehuaTimestampLayout, line, time.Local)
			if err != nil {
				return nil, fmt.Errorf("%s 第 %d 行日期无效", file, index+1)
			}
			flush()
			current = &kehuaEntry{Timestamp: stamp, File: file}
			body = nil
		} else if current != nil {
			body = append(body, line)
		} else if strings.TrimSpace(line) != "" {
			return nil, fmt.Errorf("%s 第 %d 行没有可识别的动态日期，未导入任何条目", file, index+1)
		}
	}
	flush()
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s 没有可识别的可话动态", file)
	}
	return entries, nil
}

func readKehuaFile(root *os.Root, name string, limit int64) ([]byte, error) {
	file, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err == nil && int64(len(data)) > limit {
		return nil, fmt.Errorf("文件超过 %d MB 限制", limit>>20)
	}
	return data, err
}

func importKehua(ctx context.Context, source, imageDir string, store *notes.Store) (report kehuaReport, err error) {
	report.Warnings = []string{}
	source = strings.TrimSpace(source)
	if !filepath.IsAbs(source) {
		return report, errors.New("请填写 EchoDiary 主机上已解压导出文件夹的绝对路径")
	}
	if info, statErr := os.Stat(filepath.Join(source, "我的动态")); statErr == nil && info.IsDir() {
		source = filepath.Join(source, "我的动态")
	} else if filepath.Base(filepath.Clean(source)) != "我的动态" {
		return report, errors.New("未找到「我的动态」目录，请选择可话导出包的根文件夹或「我的动态」文件夹")
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return report, fmt.Errorf("无法读取导出文件夹：%w", err)
	}
	defer root.Close()
	var textFiles []string
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".") && name != "." {
			return fs.SkipDir
		}
		if entry.Type().IsRegular() && kehuaYearFile.MatchString(entry.Name()) {
			textFiles = append(textFiles, name)
		}
		return nil
	})
	if err != nil {
		return report, fmt.Errorf("读取导出目录失败：%w", err)
	}
	if len(textFiles) == 0 {
		return report, errors.New("未找到「YYYY年-动态内容.txt」文件")
	}
	sort.Strings(textFiles)
	var entries []kehuaEntry
	for _, file := range textFiles {
		data, err := readKehuaFile(root, file, 32<<20)
		if err != nil {
			return report, fmt.Errorf("读取 %s 失败：%w", file, err)
		}
		parsed, err := parseKehuaEntries(data, file)
		if err != nil {
			return report, err
		}
		entries = append(entries, parsed...)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Timestamp.Before(entries[j].Timestamp) })
	report.Total = len(entries)
	known, err := store.ImportedKeys(ctx, "kehua")
	if err != nil {
		return report, err
	}
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		return report, err
	}
	var createdFiles []string
	defer func() {
		if err != nil {
			for _, name := range createdFiles {
				_ = os.Remove(name)
			}
		}
	}()
	var batch []notes.ImportEntry
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if known[entry.Key] {
			report.Skipped++
			continue
		}
		if strings.TrimSpace(entry.Body) == "" {
			report.Empty++
			continue
		}
		images := []string{}
		body := kehuaMedia.ReplaceAllStringFunc(entry.Body, func(marker string) string {
			match := kehuaMedia.FindStringSubmatch(marker)
			kind, name := match[1], match[2]
			warn := func(message string) string {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s · %s：%s（原引用已保留）", entry.Timestamp.Format(kehuaTimestampLayout), name, message))
				return marker
			}
			if kind == "视频" {
				return warn("MVP 暂不支持视频")
			}
			if name != path.Base(name) || strings.ContainsAny(name, `\:`) || name == "." || name == ".." {
				return warn("图片文件名无效")
			}
			mediaPath := path.Join(path.Dir(entry.File), "图片&视频", fmt.Sprintf("%d月", entry.Timestamp.Month()), name)
			data, readErr := readKehuaFile(root, mediaPath, maxImageSize)
			if readErr != nil {
				return warn("图片缺失或不可读：" + readErr.Error())
			}
			extension, _ := imageFormat(data)
			if extension == "" {
				return warn("不支持的图片格式")
			}
			digest := sha256.Sum256(data)
			imageName := fmt.Sprintf("img-%x%s", digest[:16], extension)
			destination := filepath.Join(imageDir, imageName)
			file, writeErr := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if errors.Is(writeErr, os.ErrExist) {
				stored, checkErr := os.ReadFile(destination)
				if checkErr != nil || !bytes.Equal(stored, data) {
					err = fmt.Errorf("目标图片校验失败：%s", imageName)
					return marker
				}
			} else if writeErr == nil {
				createdFiles = append(createdFiles, destination)
				_, writeErr = file.Write(data)
				closeErr := file.Close()
				if writeErr == nil {
					writeErr = closeErr
				}
			}
			if writeErr != nil && !errors.Is(writeErr, os.ErrExist) {
				err = fmt.Errorf("保存图片失败：%w", writeErr)
				return marker
			}
			url := "/media/" + imageName
			images = append(images, url)
			report.Images++
			return "[[image:" + url + "]]"
		})
		if err != nil {
			return report, err
		}
		batch = append(batch, notes.ImportEntry{Key: entry.Key, Note: notes.Note{
			ID: "kehua-" + entry.Key, Title: "", Body: body, Images: images,
			CreatedAt: "创建于：" + entry.Timestamp.Format("2006年1月2日 15:04:05"),
		}})
		known[entry.Key] = true
	}
	report.Imported, err = store.Import(ctx, "kehua", batch)
	return report, err
}

func kehuaImportHandler(state *diaryRuntime, realtime *realtimeHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var input struct {
			Path      string `json:"path"`
			LibraryID string `json:"libraryId"`
		}
		if err := decodeJSON(w, r, &input, 16<<10); err != nil || input.LibraryID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请填写导出文件夹路径并选择当前资源库"})
			return
		}
		state.importMu.Lock()
		defer state.importMu.Unlock()
		if _, _, err := state.active(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		// Pin both the database and image directory until the import commits.
		state.mu.RLock()
		handle := state.handles[input.LibraryID]
		if input.LibraryID != state.activeID || handle == nil {
			state.mu.RUnlock()
			writeJSON(w, http.StatusConflict, map[string]string{"error": "当前资源库已变更，请关闭窗口后重新导入"})
			return
		}
		report, err := importKehua(r.Context(), input.Path, handle.layout.ImageDir, handle.store)
		state.mu.RUnlock()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		report.LibraryID = input.LibraryID
		state.invalidateStats(input.LibraryID)
		realtime.publish(input.LibraryID, "notes")
		writeJSON(w, http.StatusOK, report)
	}
}
