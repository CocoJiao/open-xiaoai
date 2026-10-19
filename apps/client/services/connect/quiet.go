package connect

import (
	"bytes"
	"sync/atomic"
)

// quietScripts 服务端周期性执行的只读查询（每秒一次），逐条打日志会刷屏，
// 这里只做计数，每 quietLogEvery 次汇总一行。
var quietScripts = [][]byte{
	[]byte("player_get_play_status"),
	[]byte("mphelper mute_stat"),
}

var quietCount atomic.Int64

// IsQuietRequest 是否属于不需要逐条记录的例行查询。
func IsQuietRequest(req Request) bool {
	if req.Command != "run_shell" || req.Payload == nil {
		return false
	}
	for _, s := range quietScripts {
		if bytes.Contains(*req.Payload, s) {
			quietCount.Add(1)
			return true
		}
	}
	return false
}

// QuietCount 累计的例行查询次数（汇总日志用）。
func QuietCount() int64 { return quietCount.Load() }

// IsQuietScript 按脚本内容判断（不计数）。
func IsQuietScript(script string) bool {
	for _, s := range quietScripts {
		if bytes.Contains([]byte(script), s) {
			return true
		}
	}
	return false
}
