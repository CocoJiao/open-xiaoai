package music

import (
	"testing"
	"time"
)

func TestParsePlayStatus(t *testing.T) {
	out := `{"code":0,"info":"{\"status\":1,\"volume\":30,\"play_song_detail\":{\"position\":61000,\"duration\":245000}}"}`
	st, err := parsePlayStatus(out)
	if err != nil || st.Status != 1 || st.PositionMs != 61000 || st.DurationMs != 245000 {
		t.Fatalf("%+v %v", st, err)
	}
	st, err = parsePlayStatus(`{"code":0,"info":{"status":"0"}}`)
	if err != nil || st.Status != 0 {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := parsePlayStatus("garbage"); err == nil {
		t.Fatal("want error")
	}
}

func pollPlayer(t *testing.T, st *deviceStatus) (*Player, *[]string) {
	t.Helper()
	p, played, _ := newFeaturePlayer(t)
	p.queryStatus = func() (deviceStatus, error) { return *st, nil }
	p.SetQueue([]SongItem{{Path: "/a.mp3", URL: "u1"}, {Path: "/b.mp3", URL: "u2"}, {Path: "/c.mp3", URL: "u3"}})
	return p, played
}

func pastGrace(p *Player) {
	p.mu.Lock()
	p.lastPlayURLAt = time.Now().Add(-devicePollGrace - time.Second)
	p.mu.Unlock()
}

func TestPollAdvancesNearEnd(t *testing.T) {
	st := &deviceStatus{Status: 1, PositionMs: 100000, DurationMs: 200000}
	p, played := pollPlayer(t, st)
	pastGrace(p)
	p.pollDeviceOnce()
	if len(*played) != 1 {
		t.Fatalf("not near end, must not advance: %v", *played)
	}
	st.PositionMs = 199000
	p.pollDeviceOnce()
	if len(*played) != 2 || (*played)[1] != "u2" {
		t.Fatalf("should advance: %v", *played)
	}
	// 同一轮询周期内不会重复推进（grace 保护）
	p.pollDeviceOnce()
	if len(*played) != 2 {
		t.Fatalf("double advance: %v", *played)
	}
}

func TestPollAdvancesOnSustainedIdle(t *testing.T) {
	st := &deviceStatus{Status: 0}
	p, played := pollPlayer(t, st)
	pastGrace(p)
	p.pollDeviceOnce()
	if len(*played) != 1 {
		t.Fatal("single idle must not advance")
	}
	p.pollDeviceOnce()
	if len(*played) != 2 {
		t.Fatalf("sustained idle should advance: %v", *played)
	}
}

func TestPollDetectsHijack(t *testing.T) {
	// 这是"只能播第一首，后面就乱了"的场景：歌播完瞬间设备续播接上别的内容，
	// mute_stat 一直是 1，Idle 事件从未出现。
	st := &deviceStatus{Status: 1, PositionMs: 30000, DurationMs: 200000}
	p, played := pollPlayer(t, st)
	pastGrace(p)
	p.pollDeviceOnce()                           // 记下设备时长指纹 200s
	st.PositionMs, st.DurationMs = 2000, 1800000 // 设备换成了 30 分钟的喜马拉雅内容
	p.pollDeviceOnce()
	if len(*played) != 2 || (*played)[1] != "u2" {
		t.Fatalf("hijack should reclaim queue: %v", *played)
	}
}

func TestPollSyncsPhysicalPause(t *testing.T) {
	st := &deviceStatus{Status: 2, PositionMs: 30000, DurationMs: 200000}
	p, played := pollPlayer(t, st)
	pastGrace(p)
	p.pollDeviceOnce()
	if !p.IsPaused() {
		t.Fatal("device pause should sync")
	}
	st.Status = 1
	p.pollDeviceOnce()
	if p.IsPaused() || len(*played) != 1 {
		t.Fatalf("resume should sync without advancing: %v", *played)
	}
}

func TestPollStopsAtQueueEnd(t *testing.T) {
	st := &deviceStatus{Status: 0}
	p, _, _ := newFeaturePlayer(t)
	p.queryStatus = func() (deviceStatus, error) { return *st, nil }
	p.SetQueue([]SongItem{{Path: "/a.mp3", URL: "u1"}})
	pastGrace(p)
	p.pollDeviceOnce()
	p.pollDeviceOnce()
	if _, ok := p.CurrentSong(); ok || p.CurrentState() != StateIdle {
		t.Fatal("queue end should stop session (heartbeat must stop too)")
	}
}

// OH2P 实测：player_get_play_status 只有 status/volume/loop_type
func TestParsePlayStatusOH2P(t *testing.T) {
	st, err := parsePlayStatus("{\n\t\"code\": 0,\n\t\"info\": \"{ \\\"status\\\": 1, \\\"volume\\\": 44, \\\"loop_type\\\": 1 }\"\n}")
	if err != nil || st.Status != 1 || st.DurationMs != 0 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestPollEstimatedEndWithoutDeviceProgress(t *testing.T) {
	st := &deviceStatus{Status: 1}
	p, played, _ := newFeaturePlayer(t)
	p.queryStatus = func() (deviceStatus, error) { return *st, nil }
	p.SetQueue([]SongItem{{Path: "/a.mp3", URL: "u1", DurationMs: 200000}, {Path: "/b.mp3", URL: "u2", DurationMs: 200000}})
	setElapsed := func(d time.Duration) {
		p.mu.Lock()
		p.lastPlayURLAt = time.Now().Add(-d)
		p.mu.Unlock()
	}
	setElapsed(100 * time.Second)
	p.pollDeviceOnce()
	if len(*played) != 1 {
		t.Fatal("mid-song must not advance")
	}
	setElapsed(199 * time.Second)
	p.pollDeviceOnce()
	if len(*played) != 2 || (*played)[1] != "u2" {
		t.Fatalf("near end should advance: %v", *played)
	}
}

func TestPollEstimatedHijackAfterOverrun(t *testing.T) {
	st := &deviceStatus{Status: 1}
	p, played, _ := newFeaturePlayer(t)
	p.queryStatus = func() (deviceStatus, error) { return *st, nil }
	p.SetQueue([]SongItem{{Path: "/a.mp3", URL: "u1", DurationMs: 200000}, {Path: "/b.mp3", URL: "u2"}})
	p.mu.Lock()
	p.lastPlayURLAt = time.Now().Add(-260 * time.Second) // 早就播完，设备还在"播放"
	p.mu.Unlock()
	p.pollDeviceOnce()
	if len(*played) != 2 {
		t.Fatalf("overrun should reclaim: %v", *played)
	}
}

func TestPollEstimatedExcludesPause(t *testing.T) {
	st := &deviceStatus{Status: 1}
	p, played, _ := newFeaturePlayer(t)
	p.queryStatus = func() (deviceStatus, error) { return *st, nil }
	p.SetQueue([]SongItem{{Path: "/a.mp3", URL: "u1", DurationMs: 200000}, {Path: "/b.mp3", URL: "u2"}})
	p.mu.Lock()
	p.lastPlayURLAt = time.Now().Add(-250 * time.Second)
	p.mu.Unlock()
	p.Pause() // 设 pausedAt=now
	p.mu.Lock()
	p.pausedAt = time.Now().Add(-120 * time.Second) // 暂停了 120s，实际只播了 130s
	p.mu.Unlock()
	p.Resume()
	p.pollDeviceOnce()
	if len(*played) != 1 {
		t.Fatalf("pause time must not count: %v", *played)
	}
}
