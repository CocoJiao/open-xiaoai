package music

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FavoriteEntry 一首收藏的歌。
// 本地歌曲用 Path 唯一标识；在线歌曲没有稳定路径（直链会过期），用 Title+Singer 标识，
// 播放收藏时再重新在线搜索取直链。
type FavoriteEntry struct {
	Path    string    `json:"path,omitempty"`
	Title   string    `json:"title"`
	Singer  string    `json:"singer,omitempty"`
	Online  bool      `json:"online,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

func (e FavoriteEntry) key() string {
	if e.Online || e.Path == "" {
		return "lx:" + lxNormalize(e.Singer) + "|" + lxNormalize(e.Title)
	}
	return e.Path
}

// FavoritesStore 收藏列表的内存态 + 磁盘持久化，写法与 PlayHistoryStore 一致
// （原子写 tmp 再 rename；path 为空时 Load/Save 为 no-op）。
type FavoritesStore struct {
	mu      sync.RWMutex
	entries []FavoriteEntry
	path    string
}

// NewFavoritesStore 创建收藏存储。
func NewFavoritesStore(path string) *FavoritesStore {
	return &FavoritesStore{path: path}
}

// Load 从磁盘加载。文件不存在视为空收藏。
func (s *FavoritesStore) Load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read favorites: %w", err)
	}
	var entries []FavoriteEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse favorites: %w", err)
	}
	s.mu.Lock()
	s.entries = entries
	s.mu.Unlock()
	log.Printf("📂 [music/favorites] 已加载收藏: %d 首", len(entries))
	return nil
}

// Save 原子写入磁盘。
func (s *FavoritesStore) Save() error {
	if s.path == "" {
		return nil
	}
	s.mu.RLock()
	entries := make([]FavoriteEntry, len(s.entries))
	copy(entries, s.entries)
	s.mu.RUnlock()
	return writeJSONAtomic(s.path, entries)
}

// List 返回收藏副本（按加入顺序）。
func (s *FavoritesStore) List() []FavoriteEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]FavoriteEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

// Contains 是否已收藏。
func (s *FavoritesStore) Contains(e FavoriteEntry) bool {
	k := e.key()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, x := range s.entries {
		if x.key() == k {
			return true
		}
	}
	return false
}

// Add 加入收藏并落盘。返回 false 表示已经收藏过。
func (s *FavoritesStore) Add(e FavoriteEntry) (bool, error) {
	if e.AddedAt.IsZero() {
		e.AddedAt = time.Now()
	}
	k := e.key()
	s.mu.Lock()
	for _, x := range s.entries {
		if x.key() == k {
			s.mu.Unlock()
			return false, nil
		}
	}
	s.entries = append(s.entries, e)
	s.mu.Unlock()
	return true, s.Save()
}

// Remove 取消收藏并落盘。返回 false 表示本来就没收藏。
func (s *FavoritesStore) Remove(e FavoriteEntry) (bool, error) {
	k := e.key()
	s.mu.Lock()
	idx := -1
	for i, x := range s.entries {
		if x.key() == k {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return false, nil
	}
	s.entries = append(s.entries[:idx], s.entries[idx+1:]...)
	s.mu.Unlock()
	return true, s.Save()
}

// favoriteFromSong 把当前播放项转换成收藏条目。
func favoriteFromSong(item SongItem) FavoriteEntry {
	if item.IsOnline() {
		title, singer := item.Title, item.Singer
		if title == "" {
			title = strings.TrimPrefix(item.Path, "lx:")
		}
		return FavoriteEntry{Title: title, Singer: singer, Online: true}
	}
	return FavoriteEntry{Path: item.Path, Title: item.DisplayName(), Singer: item.Singer}
}

// writeJSONAtomic 先写同目录 tmp 再 rename，避免进程被 kill 时留下半截 JSON。
func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
