package music

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRankLXSongsPrefersSingerAndOriginal(t *testing.T) {
	songs := []lxSongInfo{
		{"name": "晴天 (伴奏)", "singer": "周杰伦"},
		{"name": "晴天", "singer": "某翻唱歌手"},
		{"name": "晴天", "singer": "周杰伦"},
	}
	got := rankLXSongs("周杰伦晴天", songs)
	if stringField(got[0], "singer") != "周杰伦" || stringField(got[0], "name") != "晴天" {
		t.Fatalf("want 周杰伦-晴天 first, got %v", got[0])
	}
	got = rankLXSongs("周杰伦的晴天", songs)
	if stringField(got[0], "singer") != "周杰伦" || stringField(got[0], "name") != "晴天" {
		t.Fatalf("的 form: want 周杰伦-晴天 first, got %v", got[0])
	}
	got = rankLXSongs("晴天", songs)
	if stringField(got[0], "name") == "晴天 (伴奏)" {
		t.Fatalf("variant should be demoted, got %v", got[0])
	}
	// 用户明确说"伴奏"时不降权
	got = rankLXSongs("晴天伴奏", songs)
	if stringField(got[0], "name") != "晴天 (伴奏)" {
		t.Fatalf("explicit 伴奏 should win, got %v", got[0])
	}
}

func TestResolveRankedFallsBackOnFailure(t *testing.T) {
	songs := []lxSongInfo{
		{"name": "稻香", "singer": "周杰伦", "id": "a"},
		{"name": "稻香", "singer": "周杰伦", "id": "b"},
	}
	calls := 0
	track, err := resolveRanked("稻香", songs, func(s lxSongInfo) (*LXTrack, error) {
		calls++
		if s["id"] == "a" {
			return nil, errors.New("dead source")
		}
		return &LXTrack{Name: "稻香", URL: "http://ok"}, nil
	})
	if err != nil || track.URL != "http://ok" || calls != 2 {
		t.Fatalf("track=%v err=%v calls=%d", track, err, calls)
	}
}

func TestFilterLXSongsBySinger(t *testing.T) {
	songs := []lxSongInfo{
		{"name": "晴天", "singer": "周杰伦"},
		{"name": "晴天 (Live)", "singer": "周杰伦"},
		{"name": "七里香", "singer": "周杰伦、费玉清"},
		{"name": "晴天", "singer": "周杰伦"},
		{"name": "别人的歌", "singer": "林俊杰"},
	}
	got := filterLXSongsBySinger("周杰伦", songs, 10)
	if len(got) != 2 {
		t.Fatalf("want 2 (dedupe + no live + no other singer), got %d: %v", len(got), got)
	}
}

func TestParseArtistIntent(t *testing.T) {
	cases := map[string]string{
		"周杰伦的歌":    "周杰伦",
		"周杰伦的歌曲":   "周杰伦",
		"歌手周杰伦的音乐": "周杰伦",
		"一些周杰伦的歌吧": "周杰伦",
		"歌曲周杰伦的歌":  "周杰伦",
		"周杰伦晴天":    "",
		"小猪佩奇的故事":  "",
		"故事周杰伦的歌":  "",
		"周杰伦的晴天":   "",
	}
	for in, want := range cases {
		got, ok := ParseArtistIntent(in)
		if (want != "") != ok || got != want {
			t.Errorf("ParseArtistIntent(%q)=%q,%v want %q", in, got, ok, want)
		}
	}
}

func newFeaturePlayer(t *testing.T) (*Player, *[]string, *[]string) {
	t.Helper()
	played := []string{}
	shells := []string{}
	p := NewPlayer(nil, nil)
	p.playURL = func(url string) error { played = append(played, url); return nil }
	p.speak = func(string) error { return nil }
	p.runShell = func(script string) (string, error) { shells = append(shells, script); return "", nil }
	return p, &played, &shells
}

func TestPlayerPauseResumeKeepsQueue(t *testing.T) {
	p, played, shells := newFeaturePlayer(t)
	p.SetQueue([]SongItem{{Path: "/a.mp3", URL: "u1"}, {Path: "/b.mp3", URL: "u2"}})
	p.mu.Lock()
	p.lastPlayURLAt = time.Now().Add(-30 * time.Second)
	p.mu.Unlock()

	if !p.Pause() {
		t.Fatal("pause failed")
	}
	if !p.IsPaused() || (*shells)[len(*shells)-1] != "mphelper pause" {
		t.Fatalf("expected paused via mphelper pause, shells=%v", *shells)
	}
	pos := p.PositionMs()
	// 暂停期间的 Idle 不能被当成播完切歌
	p.OnPlayingStatus("Idle")
	if len(*played) != 1 || !p.IsPaused() {
		t.Fatalf("idle while paused advanced queue: played=%v", *played)
	}
	time.Sleep(20 * time.Millisecond)
	if p.PositionMs() != pos {
		t.Fatalf("position must freeze while paused")
	}
	if !p.Resume() || p.IsPaused() || p.CurrentState() != StatePlaying {
		t.Fatal("resume failed")
	}
	if (*shells)[len(*shells)-1] != "mphelper play" {
		t.Fatalf("expected mphelper play, got %v", *shells)
	}
	if got := p.PositionMs(); got < pos || got > pos+500 {
		t.Fatalf("position after resume %d, before %d", got, pos)
	}
	if snap, ok := p.Snapshot(); !ok || len(snap.Remaining) != 1 {
		t.Fatalf("queue lost: %+v", snap)
	}
	if p.Resume() {
		t.Fatal("resume when not paused should return false")
	}
}

func TestPlayerLazyResolveSkipsFailures(t *testing.T) {
	p, played, _ := newFeaturePlayer(t)
	p.SetQueue([]SongItem{
		{Path: "lx:a", Resolve: func() (string, error) { return "", errors.New("dead") }},
		{Path: "lx:b", Resolve: func() (string, error) { return "http://b", nil }},
	})
	if len(*played) != 1 || (*played)[0] != "http://b" {
		t.Fatalf("expected skip to b, got %v", *played)
	}
	cur, _ := p.CurrentSong()
	if cur.Path != "lx:b" || cur.URL != "http://b" {
		t.Fatalf("current=%+v", cur)
	}
}

func TestPlayerSeekOnStartOffset(t *testing.T) {
	p, _, _ := newFeaturePlayer(t)
	var seeked int64
	p.seek = func(ms int64) error { seeked = ms; return nil }
	p.SetQueue([]SongItem{{Path: "/a.mp3", URL: "u", StartOffsetMs: 42000}})
	if seeked != 42000 {
		t.Fatalf("seek=%d", seeked)
	}
	if pos := p.PositionMs(); pos < 42000 {
		t.Fatalf("position should include offset, got %d", pos)
	}
	cur, _ := p.CurrentSong()
	if cur.StartOffsetMs != 0 {
		t.Fatal("offset must be one-shot")
	}
}

func TestFavoritesStorePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fav.json")
	s := NewFavoritesStore(path)
	e := FavoriteEntry{Path: "/music/a.mp3", Title: "a"}
	if ok, err := s.Add(e); !ok || err != nil {
		t.Fatalf("add: %v %v", ok, err)
	}
	if ok, _ := s.Add(e); ok {
		t.Fatal("duplicate add should be false")
	}
	online := favoriteFromSong(SongItem{Path: "lx:周杰伦-晴天", Title: "晴天", Singer: "周杰伦"})
	_, _ = s.Add(online)

	s2 := NewFavoritesStore(path)
	if err := s2.Load(); err != nil || len(s2.List()) != 2 {
		t.Fatalf("reload: %v %v", err, s2.List())
	}
	if !s2.Contains(FavoriteEntry{Title: "晴天", Singer: "周杰伦", Online: true}) {
		t.Fatal("online favorite not found by title+singer")
	}
	if ok, _ := s2.Remove(e); !ok || len(s2.List()) != 1 {
		t.Fatal("remove failed")
	}
}

func newFeatureModule(t *testing.T) (*Module, string, *[]string, *[]string) {
	t.Helper()
	dir := t.TempDir()
	abort := false
	cfg := &MusicConfig{
		Enabled:  true,
		Dirs:     []string{dir},
		HTTP:     HTTPConfig{Port: 18080, BaseURL: "http://music.local"},
		Commands: CommandsConfig{AbortXiaoAIOnPlay: &abort},
		History: HistoryConfig{
			File:          filepath.Join(dir, "h.json"),
			MusicFile:     filepath.Join(dir, "mh.json"),
			FavoritesFile: filepath.Join(dir, "fav.json"),
		},
	}
	cfg.ApplyDefaults()
	fileSrv := NewFileServer(&cfg.HTTP)
	fileSrv.SetBaseURL("http://music.local")
	idx := NewIndexer(cfg)
	played := []string{}
	spoken := []string{}
	p := NewPlayer(fileSrv, idx)
	p.playURL = func(url string) error { played = append(played, url); return nil }
	p.speak = func(s string) error { spoken = append(spoken, s); return nil }
	p.runShell = func(string) (string, error) { return "", nil }
	p.seek = func(int64) error { return nil }
	m := &Module{
		config:       cfg,
		indexer:      idx,
		fileSrv:      fileSrv,
		player:       p,
		history:      NewPlayHistoryStore(cfg.History.File),
		musicHistory: NewMusicHistoryStore(cfg.History.MusicFile),
		favorites:    NewFavoritesStore(cfg.History.FavoritesFile),
	}
	return m, dir, &played, &spoken
}

func TestModuleStopThenResumeFromMusicHistory(t *testing.T) {
	m, dir, played, spoken := newFeatureModule(t)
	a := filepath.Join(dir, "a.mp3")
	b := filepath.Join(dir, "b.mp3")
	for _, f := range []string{a, b} {
		if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	items := m.player.BuildQueueFromSongs([]IndexedSong{{Path: a}, {Path: b}}, "")
	m.player.SetQueue(items)
	m.player.mu.Lock()
	m.player.lastPlayURLAt = time.Now().Add(-60 * time.Second)
	m.player.mu.Unlock()

	var seeked int64
	m.player.seek = func(ms int64) error { seeked = ms; return nil }

	job := m.classifyInstruction("停止", "停止")
	job()
	if _, ok := m.player.CurrentSong(); ok {
		t.Fatal("stop should clear")
	}
	// 模拟重启：从磁盘重新加载断点
	m.musicHistory = NewMusicHistoryStore(m.config.History.MusicFile)
	if err := m.musicHistory.Load(); err != nil {
		t.Fatal(err)
	}
	m.classifyInstruction("继续播放", "继续播放")()
	if seeked < 55000 || seeked > 58000 {
		t.Fatalf("expected seek ≈57s, got %d", seeked)
	}
	snap, _ := m.player.Snapshot()
	if snap.Current.Path != a || len(snap.Remaining) != 1 || snap.Remaining[0].Path != b {
		t.Fatalf("bad resumed queue: %+v", snap)
	}
	if !strings.Contains((*spoken)[len(*spoken)-1], "接着播放") || len(*played) != 2 {
		t.Fatalf("spoken=%v played=%v", *spoken, *played)
	}
}

func TestModulePauseKeywordDoesNotClearQueue(t *testing.T) {
	m, dir, _, _ := newFeatureModule(t)
	a := filepath.Join(dir, "a.mp3")
	_ = os.WriteFile(a, []byte("x"), 0644)
	m.player.SetQueue(m.player.BuildQueueFromSongs([]IndexedSong{{Path: a}}, ""))
	m.classifyInstruction("暂停", "暂停")()
	if !m.player.IsPaused() {
		t.Fatal("暂停 should pause, not stop")
	}
	m.classifyInstruction("继续", "继续")()
	if m.player.CurrentState() != StatePlaying {
		t.Fatal("继续 should resume in place")
	}
}

func TestModuleFavoriteAddRemovePlay(t *testing.T) {
	m, dir, played, spoken := newFeatureModule(t)
	a := filepath.Join(dir, "晴天.mp3")
	_ = os.WriteFile(a, []byte("x"), 0644)
	m.player.SetQueue(m.player.BuildQueueFromSongs([]IndexedSong{{Path: a}}, ""))

	m.classifyInstruction("加入收藏", "加入收藏")()
	if (*spoken)[len(*spoken)-1] != "已收藏晴天" {
		t.Fatalf("spoken=%v", *spoken)
	}
	m.classifyInstruction("收藏", "收藏")()
	if !strings.Contains((*spoken)[len(*spoken)-1], "已经在收藏") {
		t.Fatalf("spoken=%v", *spoken)
	}
	m.player.ClearQueue()
	m.classifyInstruction("播放收藏", "播放收藏")()
	if cur, ok := m.player.CurrentSong(); !ok || cur.Path != a || len(*played) != 2 {
		t.Fatalf("play favorites failed: %v %v", cur, *played)
	}
	m.classifyInstruction("取消收藏", "取消收藏")()
	if len(m.favorites.List()) != 0 {
		t.Fatal("remove failed")
	}
}

type fakeArtistLX struct {
	fakeLXResolver
	cands    []LXCandidate
	resolved []string
}

func (f *fakeArtistLX) SearchArtistSongs(ctx context.Context, artist string, limit int) ([]LXCandidate, error) {
	return f.cands, nil
}

func (f *fakeArtistLX) ResolveCandidate(ctx context.Context, c LXCandidate) (*LXTrack, error) {
	f.resolved = append(f.resolved, c.Name)
	return &LXTrack{Name: c.Name, URL: "http://lx/" + c.Name}, nil
}

func TestModuleArtistPlayOnlineLazy(t *testing.T) {
	m, _, played, _ := newFeatureModule(t)
	lx := &fakeArtistLX{cands: []LXCandidate{{Name: "晴天", Singer: "周杰伦"}, {Name: "稻香", Singer: "周杰伦"}}}
	m.lx = lx
	m.handlePlay("周杰伦的歌")
	if len(*played) != 1 || (*played)[0] != "http://lx/晴天" {
		t.Fatalf("played=%v", *played)
	}
	if len(lx.resolved) != 1 {
		t.Fatalf("should resolve lazily, resolved=%v", lx.resolved)
	}
	m.player.Next()
	if len(lx.resolved) != 2 || (*played)[1] != "http://lx/稻香" {
		t.Fatalf("next: resolved=%v played=%v", lx.resolved, *played)
	}
}

func TestModuleArtistPlayPrefersLocal(t *testing.T) {
	m, dir, played, _ := newFeatureModule(t)
	a := filepath.Join(dir, "a.mp3")
	_ = os.WriteFile(a, []byte("x"), 0644)
	m.indexer.mu.Lock()
	m.indexer.songs = []IndexedSong{{Path: a, ArtistLower: "周杰伦"}, {Path: filepath.Join(dir, "b.mp3"), ArtistLower: "林俊杰"}}
	m.indexer.mu.Unlock()
	lx := &fakeArtistLX{}
	m.lx = lx
	m.handlePlay("周杰伦的歌")
	if len(*played) != 1 || len(lx.resolved) != 0 {
		t.Fatalf("played=%v resolved=%v", *played, lx.resolved)
	}
}
