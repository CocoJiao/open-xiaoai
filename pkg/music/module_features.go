package music

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// resumeRewind 断点续播时往回倒一点，接上之前听到的内容。
const resumeRewind = 3 * time.Second

// resumeMinPosition 播放位置小于这个值时直接从头播，不值得 seek。
const resumeMinPosition = 10 * time.Second

// recordMusicProgress 把当前播放现场写入音乐断点。分集内容交给故事历史，这里跳过。
func (m *Module) recordMusicProgress() {
	if m.musicHistory == nil || m.player == nil {
		return
	}
	snap, ok := m.player.Snapshot()
	if !ok || snap.Current.Episode > 0 {
		return
	}
	m.musicHistory.Record(snap)
}

// handlePause 真正的暂停：保留队列和位置。
func (m *Module) handlePause() bool {
	if !m.player.Pause() {
		if m.player.IsPaused() {
			m.player.Speak("已经暂停了")
		} else {
			m.player.Speak("当前没有在播放")
		}
		return true
	}
	m.recordMusicProgress()
	// 暂停后再播报：tts_play.sh 只会在原来是 Playing 时才恢复播放（见 docs/shells/README.md），
	// 所以不会把刚暂停的歌又放出来。
	m.player.Speak("好的，已暂停")
	return true
}

// handleResume "继续播放"：
//  1. 正在暂停 → 原地恢复（mphelper play）；
//  2. 否则取音乐断点和故事历史里更新的那个接着播；
//  3. 都没有 → 提示。
func (m *Module) handleResume() bool {
	if m.player.Resume() {
		return true
	}
	var music MusicHistoryEntry
	if m.musicHistory != nil {
		music = m.musicHistory.Get()
	}
	var story PlayHistoryEntry
	if m.history != nil {
		story = m.history.Get()
	}
	if story.SeriesName != "" && (music.Path == "" || story.UpdatedAt.After(music.UpdatedAt)) {
		log.Printf("▶️ [music] 继续播放: 故事历史更新，按故事续播")
		return m.handleContinueStory()
	}
	if music.Path == "" {
		m.player.Speak("还没有播放记录，不知道该接着播放什么")
		return true
	}
	return m.resumeFromMusicHistory(music)
}

func (m *Module) resumeFromMusicHistory(e MusicHistoryEntry) bool {
	offset := time.Duration(e.PositionMs) * time.Millisecond
	if offset < resumeMinPosition {
		offset = 0
	} else {
		offset -= resumeRewind
	}

	items := make([]SongItem, 0, len(e.Remaining)+1)
	var first SongItem
	haveFirst := false
	if e.Online {
		if m.lx != nil {
			title, singer := e.Title, e.Singer
			first = m.lazyOnlineItem(title, singer)
			first.StartOffsetMs = offset.Milliseconds()
			haveFirst = true
		}
	} else if item, ok := m.localItem(e.Path); ok {
		item.StartOffsetMs = offset.Milliseconds()
		first = item
		haveFirst = true
	}
	if haveFirst {
		items = append(items, first)
	}
	for _, p := range e.Remaining {
		if item, ok := m.localItem(p); ok {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		m.player.Speak("上次播放的歌曲找不到了，可能文件被移动或删除了")
		return true
	}
	name := items[0].DisplayName()
	log.Printf("▶️ [music] 按音乐断点续播: %s @%v (队列 %d 首)", items[0].Path, offset.Round(time.Second), len(items))
	if haveFirst && offset > 0 {
		_ = m.player.Speak(fmt.Sprintf("好的，接着播放%s", name))
	} else {
		_ = m.player.Speak(fmt.Sprintf("好的，继续播放%s", name))
	}
	m.maybeAbortXiaoAI()
	m.player.SetExhaustedHandler(nil)
	m.player.SetQueue(items)
	return true
}

// localItem 把本地路径变成可播放的队列项（文件不存在时 ok=false）。
func (m *Module) localItem(path string) (SongItem, bool) {
	if path == "" || m.fileSrv == nil {
		return SongItem{}, false
	}
	if _, err := os.Stat(path); err != nil {
		return SongItem{}, false
	}
	song, ok := m.indexer.SongByPath(path)
	if !ok {
		song = IndexedSong{Path: path}
	}
	items := m.player.BuildQueueFromSongs([]IndexedSong{song}, "")
	if len(items) == 0 {
		return SongItem{}, false
	}
	return items[0], true
}

// lazyOnlineItem 在线歌曲队列项：播到时才按"歌手 歌名"重新搜索取直链（直链会过期）。
func (m *Module) lazyOnlineItem(title, singer string) SongItem {
	keyword := strings.TrimSpace(singer + " " + title)
	return SongItem{
		Path:   fmt.Sprintf("lx:%s-%s", singer, title),
		Title:  title,
		Singer: singer,
		Resolve: func() (string, error) {
			track, err := m.lx.Resolve(m.context(), keyword)
			if err != nil {
				return "", err
			}
			if track == nil {
				return "", fmt.Errorf("no track")
			}
			return track.URL, nil
		},
	}
}

func (m *Module) context() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

// handleFavorite 加入/取消收藏当前歌曲。
func (m *Module) handleFavorite(add bool) bool {
	cur, ok := m.player.CurrentSong()
	if !ok {
		m.player.Speak("当前没有在播放歌曲")
		return true
	}
	if m.favorites == nil {
		m.player.Speak("收藏功能不可用")
		return true
	}
	entry := favoriteFromSong(cur)
	name := entry.Title
	var changed bool
	var err error
	if add {
		changed, err = m.favorites.Add(entry)
	} else {
		changed, err = m.favorites.Remove(entry)
	}
	if err != nil {
		log.Printf("⚠️ [music/favorites] 保存失败: %v", err)
	}
	log.Printf("❤️ [music/favorites] add=%v changed=%v entry=%+v", add, changed, entry)
	switch {
	case add && changed:
		m.player.Speak(fmt.Sprintf("已收藏%s", name))
	case add:
		m.player.Speak(fmt.Sprintf("%s已经在收藏里了", name))
	case changed:
		m.player.Speak(fmt.Sprintf("已取消收藏%s", name))
	default:
		m.player.Speak(fmt.Sprintf("%s不在收藏里", name))
	}
	return true
}

// handlePlayFavorites 播放收藏列表：本地文件直接入队，在线歌曲延迟取直链。
func (m *Module) handlePlayFavorites() bool {
	if m.favorites == nil {
		m.player.Speak("收藏功能不可用")
		return true
	}
	list := m.favorites.List()
	items := make([]SongItem, 0, len(list))
	for _, e := range list {
		if e.Online || e.Path == "" {
			if m.lx != nil {
				items = append(items, m.lazyOnlineItem(e.Title, e.Singer))
			}
			continue
		}
		if item, ok := m.localItem(e.Path); ok {
			item.Title = e.Title
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		if len(list) == 0 {
			m.player.Speak("收藏还是空的，听到喜欢的歌可以说加入收藏")
		} else {
			m.player.Speak("收藏的歌曲暂时都播放不了")
		}
		return true
	}
	_ = m.player.Speak(fmt.Sprintf("好的，播放收藏的%d首歌", len(items)))
	m.maybeAbortXiaoAI()
	m.player.SetExhaustedHandler(nil)
	m.player.SetQueue(items)
	return true
}

// handleArtistPlay "播放XX的歌"：先本地按歌手筛选，没有再在线搜这个歌手的多首歌。
func (m *Module) handleArtistPlay(artist string) bool {
	log.Printf("🎤 [music] 按歌手播放: %q", artist)
	if len(m.config.Dirs) > 0 {
		storyDirs := m.storyDirsAbs()
		songs := m.indexer.SearchByArtist(artist, m.config.Search.MaxResults)
		filtered := songs[:0]
		for _, s := range songs {
			if !m.isStorySongByDir(s.Path, storyDirs) {
				filtered = append(filtered, s)
			}
		}
		if len(filtered) > 0 {
			items := m.player.BuildQueueFromSongs(filtered, "")
			if len(items) > 0 {
				_ = m.player.Speak(fmt.Sprintf("好的，找到%d首%s的歌", len(items), artist))
				m.maybeAbortXiaoAI()
				m.player.SetExhaustedHandler(nil)
				m.player.SetQueue(items)
				return true
			}
		}
	}

	if searcher, ok := m.lx.(lxArtistSearcher); ok && m.lx != nil {
		cands, err := searcher.SearchArtistSongs(m.context(), artist, m.config.Search.MaxResults)
		if err != nil {
			log.Printf("⚠️ [music/lx] 按歌手在线搜索失败: artist=%q err=%v", artist, err)
		}
		if len(cands) > 0 {
			items := make([]SongItem, 0, len(cands))
			for _, c := range cands {
				c := c
				items = append(items, SongItem{
					Path:   fmt.Sprintf("lx:%s-%s", c.Singer, c.Name),
					Title:  c.Name,
					Singer: c.Singer,
					Resolve: func() (string, error) {
						track, err := searcher.ResolveCandidate(m.context(), c)
						if err != nil {
							return "", err
						}
						if track == nil {
							return "", fmt.Errorf("no track")
						}
						return track.URL, nil
					},
				})
			}
			m.maybeAbortXiaoAI()
			_ = m.player.Speak(fmt.Sprintf("好的，为你播放%d首%s的在线歌曲", len(items), artist))
			m.player.SetExhaustedHandler(nil)
			m.player.SetQueue(items)
			return true
		}
	}

	// 兜底：当普通关键词处理（本地模糊搜索 → 在线单曲）
	return m.playIntent(PlayIntent{SeriesName: artist}, false)
}
