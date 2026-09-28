package music

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// maxResumeQueue 断点里最多保存多少首剩余队列，避免随机播放大列表把文件撑大。
const maxResumeQueue = 50

// MusicHistoryEntry 普通音乐的播放断点（故事进度仍由 PlayHistoryStore 负责）。
// 记录当前曲 + 估算的播放位置 + 剩余本地队列，支撑"继续播放"跨重启恢复。
type MusicHistoryEntry struct {
	Path       string    `json:"path"`
	Title      string    `json:"title,omitempty"`
	Singer     string    `json:"singer,omitempty"`
	Online     bool      `json:"online,omitempty"`
	PositionMs int64     `json:"position_ms"`
	Remaining  []string  `json:"remaining,omitempty"` // 只存本地文件路径；在线直链会过期，不保存
	UpdatedAt  time.Time `json:"updated_at"`
}

// MusicHistoryStore 普通音乐断点的内存态 + 磁盘持久化。
type MusicHistoryStore struct {
	mu    sync.RWMutex
	entry MusicHistoryEntry
	path  string
}

// NewMusicHistoryStore path 为空时 Load/Save 为 no-op。
func NewMusicHistoryStore(path string) *MusicHistoryStore {
	return &MusicHistoryStore{path: path}
}

// Load 从磁盘加载，文件不存在不算错误。
func (s *MusicHistoryStore) Load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read music history: %w", err)
	}
	var e MusicHistoryEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return fmt.Errorf("parse music history: %w", err)
	}
	s.mu.Lock()
	s.entry = e
	s.mu.Unlock()
	log.Printf("📂 [music/history] 已加载音乐断点: %s @%ds (剩余 %d 首)", e.Path, e.PositionMs/1000, len(e.Remaining))
	return nil
}

// Get 返回副本；Path 为空表示没有记录。
func (s *MusicHistoryStore) Get() MusicHistoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entry
}

// Record 用播放现场更新断点并落盘。故事/分集内容（Episode>0）不记录，交给故事历史。
func (s *MusicHistoryStore) Record(snap PlaybackSnapshot) {
	cur := snap.Current
	if cur.Path == "" || cur.Episode > 0 {
		return
	}
	remaining := make([]string, 0, len(snap.Remaining))
	for _, it := range snap.Remaining {
		if it.IsOnline() || it.Episode > 0 {
			continue
		}
		remaining = append(remaining, it.Path)
		if len(remaining) >= maxResumeQueue {
			break
		}
	}
	e := MusicHistoryEntry{
		Path:       cur.Path,
		Title:      cur.Title,
		Singer:     cur.Singer,
		Online:     cur.IsOnline(),
		PositionMs: snap.PositionMs,
		Remaining:  remaining,
		UpdatedAt:  time.Now(),
	}
	s.mu.Lock()
	s.entry = e
	s.mu.Unlock()
	if s.path == "" {
		return
	}
	if err := writeJSONAtomic(s.path, e); err != nil {
		log.Printf("⚠️ [music/history] 保存音乐断点失败: %v", err)
	}
}
