package server

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"echodiary/internal/library"
	"echodiary/internal/notes"
)

const (
	passwordRounds = 310_000
	sessionCookie  = "echodiary_session"
	sessionTTL     = 14 * 24 * time.Hour
)

type adminAccount struct {
	Username   string `json:"username"`
	Salt       string `json:"salt"`
	Password   string `json:"passwordHash"`
	Iterations int    `json:"iterations"`
}

type libraryRecord struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Favorite bool   `json:"favorite"`
	Avatar   string `json:"avatar,omitempty"`
}

type savedSettings struct {
	HandoffKeys   []handoffKey    `json:"handoffKeys,omitempty"`
	Version       int             `json:"version"`
	Admin         *adminAccount   `json:"admin,omitempty"`
	SessionKey    string          `json:"sessionKey,omitempty"`
	Libraries     []libraryRecord `json:"libraries"`
	ActiveLibrary string          `json:"activeLibraryId,omitempty"`
}

type settingsFile struct {
	mu   sync.Mutex
	path string
	data savedSettings
}

func defaultSettingsPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("ECHODIARY_CONFIG")); path != "" {
		return filepath.Abs(path)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve EchoDiary configuration folder: %w", err)
	}
	return filepath.Join(base, "EchoDiary", "settings.json"), nil
}

func openSettingsFile(path string) (*settingsFile, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	store := &settingsFile{path: absolute, data: savedSettings{Version: 1, Libraries: []libraryRecord{}}}
	data, err := os.ReadFile(absolute)
	if errors.Is(err, os.ErrNotExist) {
		if err := ensureSessionKey(&store.data); err != nil {
			return nil, err
		}
		if err := store.writeLocked(store.data); err != nil {
			return nil, fmt.Errorf("initialize EchoDiary settings: %w", err)
		}
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read EchoDiary settings: %w", err)
	}
	if err := json.Unmarshal(data, &store.data); err != nil {
		return nil, fmt.Errorf("decode EchoDiary settings: %w", err)
	}
	if store.data.Version != 1 {
		return nil, fmt.Errorf("unsupported EchoDiary settings version %d", store.data.Version)
	}
	if store.data.Libraries == nil {
		store.data.Libraries = []libraryRecord{}
	}
	if store.data.SessionKey == "" {
		if err := ensureSessionKey(&store.data); err != nil {
			return nil, err
		}
		if err := store.writeLocked(store.data); err != nil {
			return nil, fmt.Errorf("save EchoDiary session key: %w", err)
		}
	} else if key, err := base64.RawURLEncoding.DecodeString(store.data.SessionKey); err != nil || len(key) != 32 {
		return nil, errors.New("EchoDiary settings contain an invalid session key")
	}
	return store, nil
}

func ensureSessionKey(settings *savedSettings) error {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("generate EchoDiary session key: %w", err)
	}
	settings.SessionKey = base64.RawURLEncoding.EncodeToString(key)
	return nil
}

func cloneSettings(value savedSettings) savedSettings {
	copyValue := value
	copyValue.HandoffKeys = append([]handoffKey(nil), value.HandoffKeys...)
	copyValue.Libraries = append([]libraryRecord(nil), value.Libraries...)
	if value.Admin != nil {
		admin := *value.Admin
		copyValue.Admin = &admin
	}
	return copyValue
}

func (s *settingsFile) snapshot() savedSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSettings(s.data)
}

func (s *settingsFile) update(change func(*savedSettings) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneSettings(s.data)
	if err := change(&next); err != nil {
		return err
	}
	if err := s.writeLocked(next); err != nil {
		return err
	}
	s.data = next
	return nil
}

func (s *settingsFile) writeLocked(value savedSettings) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".echodiary-settings-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, s.path)
}

func makeAdminAccount(username, password string) (*adminAccount, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(username) > 80 {
		return nil, errors.New("用户名不能为空，且不能超过 80 个字符")
	}
	if len(password) < 8 || len(password) > 1024 {
		return nil, errors.New("密码长度需要为 8 至 1024 个字符")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("生成密码盐失败: %w", err)
	}
	derived, err := pbkdf2.Key(sha256.New, password, salt, passwordRounds, 32)
	if err != nil {
		return nil, fmt.Errorf("生成密码摘要失败: %w", err)
	}
	return &adminAccount{
		Username: username, Salt: hex.EncodeToString(salt), Password: hex.EncodeToString(derived), Iterations: passwordRounds,
	}, nil
}

func verifyPassword(account *adminAccount, username, password string) bool {
	if account == nil || !strings.EqualFold(strings.TrimSpace(username), account.Username) || account.Iterations < 100_000 || account.Iterations > 2_000_000 {
		return false
	}
	salt, saltErr := hex.DecodeString(account.Salt)
	want, hashErr := hex.DecodeString(account.Password)
	if saltErr != nil || hashErr != nil || len(salt) < 16 || len(want) != 32 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, account.Iterations, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

type sessions struct {
	secret []byte
}

type sessionClaims struct {
	Username  string `json:"username"`
	ExpiresAt int64  `json:"expiresAt"`
	Nonce     string `json:"nonce"`
}

func newSessions(settings *settingsFile) *sessions {
	snapshot := settings.snapshot()
	secret, _ := base64.RawURLEncoding.DecodeString(snapshot.SessionKey)
	return &sessions{secret: secret}
}

func (s *sessions) create(username string) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	claims, err := json.Marshal(sessionClaims{
		Username: username, ExpiresAt: time.Now().Add(sessionTTL).Unix(), Nonce: hex.EncodeToString(nonce),
	})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + signature, nil
}

func (s *sessions) username(token string) (string, bool) {
	if token == "" || len(token) > 4096 || len(s.secret) != 32 {
		return "", false
	}
	payload, signature, found := strings.Cut(token, ".")
	if !found || strings.Contains(signature, ".") {
		return "", false
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload))
	if subtle.ConstantTimeCompare(provided, mac.Sum(nil)) != 1 {
		return "", false
	}
	encodedClaims, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", false
	}
	var claims sessionClaims
	if err := json.Unmarshal(encodedClaims, &claims); err != nil || strings.TrimSpace(claims.Username) == "" || claims.Nonce == "" || claims.ExpiresAt <= time.Now().Unix() {
		return "", false
	}
	return claims.Username, true
}

func sessionToken(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil,
		SameSite: http.SameSiteStrictMode, Expires: time.Now().Add(sessionTTL), MaxAge: int(sessionTTL.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: r.TLS != nil,
		SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

type libraryHandle struct {
	layout  library.Layout
	store   *notes.Store
	statsMu sync.Mutex
	statsAt time.Time
	stats   cachedLibraryStats
}

type cachedLibraryStats struct {
	notes      int
	images     int
	favorites  int
	dataBytes  int64
	modifiedAt time.Time
}

type diaryRuntime struct {
	mu       sync.RWMutex
	importMu sync.Mutex
	settings *settingsFile
	entries  []libraryRecord
	handles  map[string]*libraryHandle
	activeID string
}

func newDiaryRuntime(settings *settingsFile, initial *libraryHandle, forceInitial bool) (*diaryRuntime, error) {
	snapshot := settings.snapshot()
	state := &diaryRuntime{settings: settings, entries: append([]libraryRecord(nil), snapshot.Libraries...), handles: map[string]*libraryHandle{}}
	if initial != nil && initial.layout.Manifest.ID != "" {
		state.handles[initial.layout.Manifest.ID] = initial
		found := false
		for index := range state.entries {
			if state.entries[index].ID == initial.layout.Manifest.ID {
				state.entries[index].Path = initial.layout.Root
				if state.entries[index].Name == "" {
					state.entries[index].Name = initial.layout.Manifest.Name
				}
				found = true
				break
			}
		}
		if !found {
			state.entries = append(state.entries, libraryRecord{ID: initial.layout.Manifest.ID, Name: initial.layout.Manifest.Name, Path: initial.layout.Root})
		}
		if forceInitial || snapshot.ActiveLibrary == "" {
			state.activeID = initial.layout.Manifest.ID
		}
	}
	if state.activeID == "" {
		state.activeID = snapshot.ActiveLibrary
	}
	if !state.hasEntry(state.activeID) && len(state.entries) > 0 {
		state.activeID = state.entries[0].ID
	}
	if snapshot.Admin != nil && (snapshot.ActiveLibrary != state.activeID || !sameLibraryRecords(snapshot.Libraries, state.entries)) {
		if err := settings.update(func(next *savedSettings) error {
			next.Libraries = append([]libraryRecord(nil), state.entries...)
			next.ActiveLibrary = state.activeID
			return nil
		}); err != nil {
			return nil, fmt.Errorf("save active diary library: %w", err)
		}
	}
	return state, nil
}

func sameLibraryRecords(left, right []libraryRecord) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (s *diaryRuntime) hasEntry(id string) bool {
	for _, entry := range s.entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func (s *diaryRuntime) active() (library.Layout, *notes.Store, error) {
	s.mu.RLock()
	id := s.activeID
	handle := s.handles[id]
	s.mu.RUnlock()
	if handle == nil {
		var err error
		handle, err = s.openHandle(id)
		if err != nil {
			return library.Layout{}, nil, err
		}
	}
	return handle.layout, handle.store, nil
}

func (s *diaryRuntime) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for _, handle := range s.handles {
		if handle.store != nil {
			if err := handle.store.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (s *diaryRuntime) openHandle(id string) (*libraryHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if handle := s.handles[id]; handle != nil {
		return handle, nil
	}
	var record *libraryRecord
	for index := range s.entries {
		if s.entries[index].ID == id {
			copyRecord := s.entries[index]
			record = &copyRecord
			break
		}
	}
	if record == nil {
		return nil, errors.New("资源库不存在")
	}
	layout, err := library.Load(record.Path)
	if err != nil {
		return nil, err
	}
	if layout.Manifest.ID != record.ID {
		return nil, errors.New("资源库身份与登记信息不符")
	}
	store, err := notes.OpenForLibrary(layout.DataDir)
	if err != nil {
		return nil, err
	}
	handle := &libraryHandle{layout: layout, store: store}
	s.handles[id] = handle
	return handle, nil
}

func (s *diaryRuntime) activate(id string) error {
	if _, err := s.openHandle(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeID == id {
		return nil
	}
	if s.settings.snapshot().Admin != nil {
		if err := s.settings.update(func(next *savedSettings) error {
			if !containsLibrary(next.Libraries, id) {
				return errors.New("资源库尚未登记")
			}
			next.ActiveLibrary = id
			return nil
		}); err != nil {
			return err
		}
	}
	s.activeID = id
	return nil
}

func containsLibrary(entries []libraryRecord, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func (s *diaryRuntime) prepareLibrary(root, name string) (libraryRecord, *libraryHandle, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return libraryRecord{}, nil, errors.New("请输入资源库文件夹的主机路径")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return libraryRecord{}, nil, err
	}
	s.mu.RLock()
	for _, entry := range s.entries {
		if filepath.Clean(entry.Path) == filepath.Clean(absolute) {
			s.mu.RUnlock()
			return libraryRecord{}, nil, errors.New("此文件夹已经添加为资源库")
		}
	}
	s.mu.RUnlock()
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return libraryRecord{}, nil, fmt.Errorf("创建资源库文件夹失败: %w", err)
	}
	layout, err := library.Open(absolute)
	if err != nil {
		return libraryRecord{}, nil, err
	}
	s.mu.RLock()
	for _, entry := range s.entries {
		if entry.ID == layout.Manifest.ID {
			s.mu.RUnlock()
			return libraryRecord{}, nil, errors.New("这个资源库已经从其他路径登记，不能重复添加同一份资料库")
		}
	}
	s.mu.RUnlock()
	if name = strings.TrimSpace(name); name != "" && name != layout.Manifest.Name {
		layout, err = library.Rename(layout.Root, name)
		if err != nil {
			return libraryRecord{}, nil, fmt.Errorf("重命名资源库失败: %w", err)
		}
	}
	store, err := notes.OpenForLibrary(layout.DataDir)
	if err != nil {
		return libraryRecord{}, nil, fmt.Errorf("打开资源库数据库失败: %w", err)
	}
	return libraryRecord{ID: layout.Manifest.ID, Name: layout.Manifest.Name, Path: layout.Root}, &libraryHandle{layout: layout, store: store}, nil
}

func (s *diaryRuntime) prepareSetupLibrary(root, name string) (libraryRecord, *libraryHandle, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return libraryRecord{}, nil, errors.New("请输入资源库文件夹的主机路径")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return libraryRecord{}, nil, err
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return libraryRecord{}, nil, fmt.Errorf("创建资源库文件夹失败: %w", err)
	}
	layout, err := library.Open(absolute)
	if err != nil {
		return libraryRecord{}, nil, err
	}
	s.mu.RLock()
	for _, entry := range s.entries {
		if entry.Path != layout.Root && entry.ID == layout.Manifest.ID {
			s.mu.RUnlock()
			return libraryRecord{}, nil, errors.New("这个资源库已经从其他路径登记，不能重复添加同一份资料库")
		}
	}
	handle := s.handles[layout.Manifest.ID]
	s.mu.RUnlock()
	if name = strings.TrimSpace(name); name != "" && name != layout.Manifest.Name {
		layout, err = library.Rename(layout.Root, name)
		if err != nil {
			return libraryRecord{}, nil, fmt.Errorf("重命名资源库失败: %w", err)
		}
	}
	if handle != nil {
		handle.layout = layout
		return libraryRecord{ID: layout.Manifest.ID, Name: layout.Manifest.Name, Path: layout.Root}, handle, nil
	}
	store, err := notes.OpenForLibrary(layout.DataDir)
	if err != nil {
		return libraryRecord{}, nil, fmt.Errorf("打开资源库数据库失败: %w", err)
	}
	return libraryRecord{ID: layout.Manifest.ID, Name: layout.Manifest.Name, Path: layout.Root}, &libraryHandle{layout: layout, store: store}, nil
}

func (s *diaryRuntime) installSetup(account *adminAccount, record libraryRecord, handle *libraryHandle) error {
	if err := s.settings.update(func(next *savedSettings) error {
		if next.Admin != nil {
			return errors.New("EchoDiary 已完成初始化，请登录")
		}
		next.Admin = account
		next.Libraries = []libraryRecord{record}
		next.ActiveLibrary = record.ID
		return nil
	}); err != nil {
		return err
	}
	s.mu.Lock()
	s.entries = []libraryRecord{record}
	s.handles[record.ID] = handle
	s.activeID = record.ID
	s.mu.Unlock()
	return nil
}

func (s *diaryRuntime) addLibrary(root, name string) (libraryRecord, error) {
	record, handle, err := s.prepareLibrary(root, name)
	if err != nil {
		return libraryRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if containsLibrary(s.entries, record.ID) {
		_ = handle.store.Close()
		return libraryRecord{}, errors.New("此资源库已经添加")
	}
	nextEntries := append(append([]libraryRecord(nil), s.entries...), record)
	if err := s.settings.update(func(next *savedSettings) error {
		if next.Admin == nil {
			return errors.New("请先初始化 EchoDiary")
		}
		next.Libraries = append([]libraryRecord(nil), nextEntries...)
		next.ActiveLibrary = record.ID
		return nil
	}); err != nil {
		_ = handle.store.Close()
		return libraryRecord{}, err
	}
	s.entries = nextEntries
	s.handles[record.ID] = handle
	s.activeID = record.ID
	return record, nil
}

func (s *diaryRuntime) setFavorite(id string, favorite bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !containsLibrary(s.entries, id) {
		return errors.New("资源库不存在")
	}
	nextEntries := append([]libraryRecord(nil), s.entries...)
	for index := range nextEntries {
		if nextEntries[index].ID == id {
			nextEntries[index].Favorite = favorite
		}
	}
	if err := s.settings.update(func(next *savedSettings) error {
		next.Libraries = append([]libraryRecord(nil), nextEntries...)
		return nil
	}); err != nil {
		return err
	}
	s.entries = nextEntries
	return nil
}

func (s *diaryRuntime) setAvatar(id, avatar string) error {
	if len(avatar) > 4<<20 || (avatar != "" && !strings.HasPrefix(avatar, "data:image/")) {
		return errors.New("资源库头像格式无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !containsLibrary(s.entries, id) {
		return errors.New("资源库不存在")
	}
	nextEntries := append([]libraryRecord(nil), s.entries...)
	for index := range nextEntries {
		if nextEntries[index].ID == id {
			nextEntries[index].Avatar = avatar
		}
	}
	if err := s.settings.update(func(next *savedSettings) error {
		next.Libraries = append([]libraryRecord(nil), nextEntries...)
		return nil
	}); err != nil {
		return err
	}
	s.entries = nextEntries
	return nil
}

func (s *diaryRuntime) invalidateStats(id string) {
	s.mu.RLock()
	handle := s.handles[id]
	s.mu.RUnlock()
	if handle != nil {
		handle.statsMu.Lock()
		handle.statsAt = time.Time{}
		handle.statsMu.Unlock()
	}
}

func (s *diaryRuntime) invalidateActiveStats() {
	s.mu.RLock()
	id := s.activeID
	s.mu.RUnlock()
	s.invalidateStats(id)
}

func (s *diaryRuntime) rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return errors.New("资源库名称不能为空，且不能超过 120 个字符")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var record *libraryRecord
	for index := range s.entries {
		if s.entries[index].ID == id {
			copyRecord := s.entries[index]
			record = &copyRecord
			break
		}
	}
	if record == nil {
		return errors.New("资源库不存在")
	}
	layout, err := library.Rename(record.Path, name)
	if err != nil {
		return err
	}
	nextEntries := append([]libraryRecord(nil), s.entries...)
	for index := range nextEntries {
		if nextEntries[index].ID == id {
			nextEntries[index].Name = name
		}
	}
	if err := s.settings.update(func(next *savedSettings) error {
		next.Libraries = append([]libraryRecord(nil), nextEntries...)
		return nil
	}); err != nil {
		return err
	}
	s.entries = nextEntries
	if handle := s.handles[id]; handle != nil {
		handle.layout = layout
	}
	return nil
}

// relocate only re-registers an existing portable library at a new host path.
// The destination must already contain the same manifest ID, so this action
// never creates, moves, overwrites, or silently replaces library data.
func (s *diaryRuntime) relocate(id, root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return errors.New("请输入资源库文件夹路径")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	layout, err := library.Load(absolute)
	if err != nil {
		return fmt.Errorf("打开目标资源库失败: %w", err)
	}
	if layout.Manifest.ID != id {
		return errors.New("目标文件夹不是同一个 EchoDiary 资源库")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i := range s.entries {
		if s.entries[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("资源库不存在")
	}
	if filepath.Clean(s.entries[index].Path) == filepath.Clean(layout.Root) {
		return nil
	}
	for i, entry := range s.entries {
		if i != index && filepath.Clean(entry.Path) == filepath.Clean(layout.Root) {
			return errors.New("目标文件夹已经登记为其他资源库")
		}
	}

	store, err := notes.OpenForLibrary(layout.DataDir)
	if err != nil {
		return fmt.Errorf("打开目标资源库数据库失败: %w", err)
	}
	nextEntries := append([]libraryRecord(nil), s.entries...)
	nextEntries[index].Path = layout.Root
	if err := s.settings.update(func(next *savedSettings) error {
		next.Libraries = append([]libraryRecord(nil), nextEntries...)
		return nil
	}); err != nil {
		_ = store.Close()
		return err
	}

	old := s.handles[id]
	s.entries = nextEntries
	s.handles[id] = &libraryHandle{layout: layout, store: store}
	if old != nil && old.store != nil {
		_ = old.store.Close()
	}
	return nil
}

type libraryCard struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Favorite   bool      `json:"favorite"`
	Avatar     string    `json:"avatar,omitempty"`
	Active     bool      `json:"active"`
	Available  bool      `json:"available"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"createdAt,omitempty"`
	ModifiedAt time.Time `json:"modifiedAt,omitempty"`
	Notes      int       `json:"notes"`
	Images     int       `json:"images"`
	Favorites  int       `json:"favorites"`
	DataBytes  int64     `json:"dataBytes"`
}

func (s *diaryRuntime) listLibraries(forceRefresh ...bool) []libraryCard {
	force := len(forceRefresh) > 0 && forceRefresh[0]
	s.mu.RLock()
	entries := append([]libraryRecord(nil), s.entries...)
	active := s.activeID
	s.mu.RUnlock()
	result := make([]libraryCard, 0, len(entries))
	for _, entry := range entries {
		card := libraryCard{ID: entry.ID, Name: entry.Name, Path: entry.Path, Favorite: entry.Favorite, Avatar: entry.Avatar, Active: active == entry.ID}
		handle, err := s.openHandle(entry.ID)
		if err != nil {
			card.Error = err.Error()
			result = append(result, card)
			continue
		}
		card.Available = true
		card.CreatedAt = handle.layout.Manifest.CreatedAt
		card.Name = handle.layout.Manifest.Name
		stats := libraryStats(handle, force)
		card.Notes, card.Images, card.Favorites = stats.notes, stats.images, stats.favorites
		card.DataBytes, card.ModifiedAt = stats.dataBytes, stats.modifiedAt
		result = append(result, card)
	}
	return result
}

func libraryStats(handle *libraryHandle, force bool) cachedLibraryStats {
	handle.statsMu.Lock()
	defer handle.statsMu.Unlock()
	if !force && time.Since(handle.statsAt) < 30*time.Second {
		return handle.stats
	}
	stats := cachedLibraryStats{}
	if notesCount, imagesCount, favoriteCount, err := handle.store.Counts(); err == nil {
		stats.notes, stats.images, stats.favorites = notesCount, imagesCount, favoriteCount
	} else if allNotes, listErr := handle.store.List(); listErr == nil {
		stats.notes = len(allNotes)
		for _, note := range allNotes {
			stats.images += len(note.Images)
			if note.Favorite {
				stats.favorites++
			}
		}
	}
	stats.dataBytes, stats.modifiedAt = directoryStats(handle.layout.Root)
	handle.stats, handle.statsAt = stats, time.Now()
	return stats
}

func directoryStats(root string) (int64, time.Time) {
	var total int64
	var latest time.Time
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		total += info.Size()
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	return total, latest
}

func authRequired(settings *settingsFile, sessions *sessions, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/api/handoff/notes" || path == "/api/handoff/append" {
			next.ServeHTTP(w, r)
			return
		}
		if !(strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/media/")) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !enforceSameOrigin(r) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "请求来源无效"})
			return
		}
		if path == "/api/auth/status" || path == "/api/auth/setup" || path == "/api/auth/login" || path == "/api/auth/logout" {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := sessions.username(sessionToken(r)); !ok || settings.snapshot().Admin == nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "请先登录"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackClient(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = strings.Trim(strings.TrimSpace(r.RemoteAddr), "[]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func enforceSameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := http.NewRequest(http.MethodGet, origin, nil)
	if err != nil || parsed.URL.Host == "" || !strings.EqualFold(parsed.URL.Host, r.Host) {
		return false
	}
	if parsed.URL.Scheme == "https" {
		return r.TLS != nil
	}
	return parsed.URL.Scheme == "http" && r.TLS == nil
}
