package music

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// 设备状态轮询（电平触发）。
//
// 为什么需要它：原来的切歌完全依赖客户端 PlayingMonitor 上报的"状态变化"事件
// （每 200ms 查一次 mute_stat，只有状态变了才上报）。这是边沿触发，有两个致命问题：
//  1. 歌曲结束后，设备自带的续播（喜马拉雅 CP 恢复、云端 PlayList 下一项）常在 200ms
//     内接上别的内容，mute_stat 一直是 1，Idle 根本没被看到 → 我们的队列永远不推进，
//     设备在播别的东西（"只能播第一首，后面就乱了"）。
//  2. 事件丢了就再也不会重发，服务端 state 和设备真实状态从此分叉。
//
// 新做法：服务端自己每秒查一次 `player_get_play_status`，拿到 status + 播放位置 + 时长：
//   - 快播完（剩余 <= 提前量）→ 抢在设备续播之前主动切下一首（所有格式都有效，不再只限 mp3）；
//   - 设备 Idle（连续两次）→ 切下一首，不怕漏事件；
//   - 设备在播的时长和我们这首对不上 → 判定被设备/云端接管，立刻把队列抢回来；
//   - 设备被物理键暂停/恢复 → 同步成 StatePaused/Playing，"继续"可以原地恢复。
// 边沿事件（OnPlayingStatus）仍然保留作为补充；两边谁先触发都只会推进一次（靠 lastPlayURLAt 校验）。

const (
	devicePollGrace        = 5 * time.Second // PlayURL 后这段时间设备状态不稳定，不做判断
	devicePollIdleCount    = 2               // 连续几次 Idle 才算真的停了
	devicePollDurTolerance = 5 * time.Second // 设备时长与预期差多少以上算"换了内容"
	devicePollMinPreempt   = 1500 * time.Millisecond
)

// deviceStatus player_get_play_status 解析结果。Status: 0 空闲 / 1 播放 / 2 暂停。
type deviceStatus struct {
	Status     int
	PositionMs int64
	DurationMs int64
}

// parsePlayStatus 解析 `ubus call mediaplayer player_get_play_status` 输出：
// 外层 {"code":0,"info":"<json 字符串>"}，内层含 status 和 play_song_detail{position,duration}。
// 对 info 是对象、字段是字符串数字等情况都做了兼容。
func parsePlayStatus(out string) (deviceStatus, error) {
	var outer map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &outer); err != nil {
		return deviceStatus{}, fmt.Errorf("outer json: %w", err)
	}
	raw, ok := outer["info"]
	if !ok {
		return deviceStatus{}, fmt.Errorf("no info field")
	}
	var infoStr string
	if json.Unmarshal(raw, &infoStr) == nil {
		raw = json.RawMessage(infoStr)
	}
	var info map[string]any
	if err := json.Unmarshal(raw, &info); err != nil {
		return deviceStatus{}, fmt.Errorf("info json: %w", err)
	}
	st := deviceStatus{Status: int(anyInt(info["status"]))}
	if d, ok := info["play_song_detail"].(map[string]any); ok {
		st.PositionMs = anyInt(d["position"])
		st.DurationMs = anyInt(d["duration"])
	}
	return st, nil
}

func anyInt(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		var n int64
		fmt.Sscan(x, &n)
		return n
	case json.Number:
		n, _ := x.Int64()
		return n
	}
	return 0
}

// queryDeviceStatus 查询设备当前播放状态（测试可通过 p.queryStatus 覆盖）。
func (p *Player) queryDeviceStatus() (deviceStatus, error) {
	if p.queryStatus != nil {
		return p.queryStatus()
	}
	out, err := p.runShellCommand("ubus -t 2 call mediaplayer player_get_play_status 2>/dev/null", 3000)
	if err != nil {
		return deviceStatus{}, err
	}
	return parsePlayStatus(out)
}

// DeviceStatusLoop 每 interval 轮询一次设备状态并驱动队列，阻塞直到 ctx 取消。
func (p *Player) DeviceStatusLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		log.Printf("📡 [music/player] 设备状态轮询已禁用")
		return
	}
	log.Printf("📡 [music/player] 设备状态轮询已启用: 间隔=%v 提前量=%v", interval, p.preemptMargin)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.pollDeviceOnce()
		}
	}
}

// pollDeviceOnce 单次轮询。查询在锁外进行，回来后校验"还是同一首、期间没被别人切过"再动作。
func (p *Player) pollDeviceOnce() {
	p.mu.Lock()
	if p.currentSong == nil || (p.state != StatePlaying && p.state != StatePaused) {
		p.pollIdle = 0
		p.mu.Unlock()
		return
	}
	now := time.Now()
	if (!p.suppressUntil.IsZero() && now.Before(p.suppressUntil)) || now.Sub(p.lastPlayURLAt) < devicePollGrace {
		p.mu.Unlock()
		return
	}
	stamp := p.lastPlayURLAt
	p.mu.Unlock()

	st, err := p.queryDeviceStatus()
	if err != nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.currentSong == nil || !p.lastPlayURLAt.Equal(stamp) {
		return // 期间已切歌/停止
	}
	song := p.currentSong

	// 记住设备报告的时长作为这首歌的"指纹"（非 mp3 也能拿到时长）。
	if st.Status == 1 && st.DurationMs > 0 && p.deviceDurMs == 0 {
		expected := song.DurationMs
		if expected <= 0 || absDur(st.DurationMs-expected) < devicePollDurTolerance.Milliseconds() {
			p.deviceDurMs = st.DurationMs
		} else {
			p.deviceDurMs = expected
		}
	}

	switch st.Status {
	case 2: // 设备暂停（物理键 / 小爱原生 TTS 抢占）
		p.pollIdle = 0
		if p.state == StatePlaying {
			log.Printf("⏸️ [music/player] 轮询: 设备处于暂停，同步为 Paused（说\"继续\"可原地恢复）")
			p.state = StatePaused
			p.pausedAt = now
		}
		return
	case 1:
		p.pollIdle = 0
		if p.state == StatePaused {
			log.Printf("▶️ [music/player] 轮询: 设备已恢复播放")
			p.markResumedLocked(now)
		}
		if p.deviceDurMs > 0 && st.DurationMs > 0 && absDur(st.DurationMs-p.deviceDurMs) >= devicePollDurTolerance.Milliseconds() {
			log.Printf("🚨 [music/player] 轮询: 设备在播的内容时长=%ds 与当前曲 %s (%ds) 不符，判定被设备/云端接管，抢回队列",
				st.DurationMs/1000, song.Path, p.deviceDurMs/1000)
			p.advanceLocked("被接管")
			return
		}
		if st.DurationMs <= 0 {
			// 固件（如 OH2P）不返回 position/duration：用已知时长 + 已播时间估算。
			p.checkEstimatedEndLocked(now)
			return
		}
		if st.DurationMs > 0 && st.PositionMs > 0 {
			// 用设备真实位置校准进度（断点续播更准）
			p.currentOffset = time.Duration(st.PositionMs)*time.Millisecond - now.Sub(p.lastPlayURLAt)
			margin := p.preemptMargin
			if margin < devicePollMinPreempt {
				margin = devicePollMinPreempt
			}
			if st.DurationMs-st.PositionMs <= margin.Milliseconds() {
				log.Printf("⏭️ [music/player] 轮询: %s 即将播完 (%ds/%ds)，抢在设备续播前切下一首",
					song.Path, st.PositionMs/1000, st.DurationMs/1000)
				p.advanceLocked("即将播完")
			}
		}
	default: // 0: 空闲
		if p.state != StatePlaying {
			return
		}
		p.pollIdle++
		if p.pollIdle >= devicePollIdleCount {
			log.Printf("⏭️ [music/player] 轮询: 设备已空闲 (连续 %d 次)，切下一首", p.pollIdle)
			p.advanceLocked("设备空闲")
		}
	}
}

// advanceLocked 按播放模式推进到下一首（调用方持锁）。
func (p *Player) advanceLocked(reason string) {
	p.pollIdle = 0
	var ok bool
	if p.mode == PlaybackModeRepeatOne {
		ok = p.replayCurrentLocked()
	} else {
		ok = p.nextLocked(true)
	}
	if ok {
		p.state = StatePlaying
		return
	}
	log.Printf("⏹️ [music/player] 队列已播完 (%s)", reason)
	p.state = StateIdle
	p.currentSong = nil
}

func absDur(d int64) int64 {
	if d < 0 {
		return -d
	}
	return d
}

// devicePollOverrun 估算位置超过时长这么久设备还在"播放"，说明歌早播完了、
// 设备续播接上了别的内容（mute_stat 一直是 1，Idle 从没出现）→ 抢回队列。
const devicePollOverrun = 3 * time.Second

// checkEstimatedEndLocked 设备不报进度时的兜底：按 DurationMs（索引时从 mp3 帧头算出）
// 和已播时间（扣除暂停、含断点偏移）判断。快播完时提前切；超时仍在播判定被接管。
// 时长未知（非 mp3/在线歌）时无法判断，只能依赖 Idle。
func (p *Player) checkEstimatedEndLocked(now time.Time) {
	song := p.currentSong
	if song == nil || song.DurationMs <= 0 {
		return
	}
	dur := time.Duration(song.DurationMs) * time.Millisecond
	elapsed := p.currentOffset + now.Sub(p.lastPlayURLAt)
	margin := p.preemptMargin
	if margin < devicePollMinPreempt {
		margin = devicePollMinPreempt
	}
	if maxM := time.Duration(float64(dur) * preemptMaxMarginRatio); margin > maxM {
		margin = maxM
	}
	switch {
	case elapsed >= dur+devicePollOverrun:
		log.Printf("🚨 [music/player] 轮询(估算): %s 应在 %v 播完，已过 %v 设备仍在播放，判定被设备续播接管，抢回队列",
			song.Path, dur.Round(time.Second), elapsed.Round(time.Second))
		p.advanceLocked("被接管")
	case elapsed >= dur-margin:
		log.Printf("⏭️ [music/player] 轮询(估算): %s 即将播完 (%v/%v)，提前切下一首",
			song.Path, elapsed.Round(time.Second), dur.Round(time.Second))
		p.advanceLocked("即将播完")
	}
}
