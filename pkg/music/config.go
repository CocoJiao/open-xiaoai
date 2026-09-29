package music

import "strings"

// MusicConfig 音乐模块配置
type MusicConfig struct {
	Enabled    bool           `yaml:"enabled"`
	Dirs       []string       `yaml:"dirs"`
	Extensions []string       `yaml:"extensions"`
	Search     SearchConfig   `yaml:"search"`
	Commands   CommandsConfig `yaml:"commands"`
	HTTP       HTTPConfig     `yaml:"http"`
	LX         LXConfig       `yaml:"lx"`
	Player     PlayerConfig   `yaml:"player"`
	Stories    []StoryConfig  `yaml:"stories"` // 故事/有声书分类，用于精确匹配与集数解析
	History    HistoryConfig  `yaml:"history"` // 播放历史（"继续播放故事"依赖的持久化记录）
}

// HistoryConfig 播放历史持久化配置。
// 只针对故事/有声书：记录"最近一次听的系列名 + 集数"，支持跨重启的
// "继续播放故事"语音指令。普通音乐/在线歌曲不记录（没有"集"的概念）。
type HistoryConfig struct {
	File string `yaml:"file"` // 历史记录文件路径，空则用默认值 cache/play_history.json
	// MusicFile 普通音乐播放断点（"继续播放"用），空则用 cache/music_history.json
	MusicFile string `yaml:"music_file"`
	// FavoritesFile 收藏列表，空则用 cache/favorites.json
	FavoritesFile string `yaml:"favorites_file"`
}

// StoryConfig 故事/有声书配置
type StoryConfig struct {
	Name           string   `yaml:"name"`            // 系列名，如「西游记」
	Aliases        []string `yaml:"aliases"`         // 别名，如「西游」
	Dir            string   `yaml:"dir"`             // 限定目录（可选），空则在 dirs 下搜索
	EpisodePattern string   `yaml:"episode_pattern"` // 集数正则，如 `第?(\\d+)[集回]`，空则用默认
}

// SearchConfig 搜索与索引配置
type SearchConfig struct {
	MaxResults         int     `yaml:"max_results"`
	RefreshIntervalSec float64 `yaml:"refresh_interval_sec"`
	IndexFile          string  `yaml:"index_file"`
}

// CommandsConfig 指令关键词配置
type CommandsConfig struct {
	PlayKeywords        []string `yaml:"play_keywords"`
	DownloadKeywords    []string `yaml:"download_keywords"`
	StopKeywords        []string `yaml:"stop_keywords"`
	NextKeywords        []string `yaml:"next_keywords"`
	PreviousKeywords    []string `yaml:"previous_keywords"`
	RefreshKeywords     []string `yaml:"refresh_keywords"`
	RandomPlayKeywords  []string `yaml:"random_play_keywords"`
	RepeatOneKeywords   []string `yaml:"repeat_one_keywords"`
	RepeatAllKeywords   []string `yaml:"repeat_all_keywords"`
	ShuffleModeKeywords []string `yaml:"shuffle_mode_keywords"`

	// ContinueStoryKeywords："继续播放故事"类口令：读取 play_history.json 里记录的
	// 最近一次系列名+集数，自动接着播放，不需要用户报出系列名。只针对故事/有声书场景，
	// 刻意不做成通用的"继续播放"（避免和普通音乐/暂停恢复语义混淆）。
	ContinueStoryKeywords []string `yaml:"continue_story_keywords"`

	// PauseKeywords 真正的暂停：保留队列和进度，"继续播放"可原地恢复。
	// 优先级高于 stop_keywords（旧配置里 stop_keywords 含"暂停"也会按暂停处理）。
	PauseKeywords []string `yaml:"pause_keywords"`
	// ResumeKeywords 明确的"继续播放"：任何时候都生效。暂停中原地恢复；否则按音乐断点
	// （或故事历史，取更新的那个）接着播。
	ResumeKeywords []string `yaml:"resume_keywords"`
	// ResumeShortKeywords 短口令"继续/接着听"：只在音乐会话活跃时生效（见 SessionOnlyShortCommands）。
	ResumeShortKeywords []string `yaml:"resume_short_keywords"`

	// SessionOnlyShortCommands 为 true（默认）时，短控制口令（暂停、继续、下一首、上一首、
	// 收藏/取消收藏）只在音乐会话活跃（有当前曲，播放中或暂停中）时才由音乐模块接管；
	// 否则交还给上层 AI/小爱，避免聊天时说"继续""下一个"被音乐模块截胡。
	SessionOnlyShortCommands *bool `yaml:"session_only_short_commands,omitempty"`
	// FavoriteAddKeywords / FavoriteRemoveKeywords 收藏/取消收藏当前歌曲
	FavoriteAddKeywords    []string `yaml:"favorite_add_keywords"`
	FavoriteRemoveKeywords []string `yaml:"favorite_remove_keywords"`
	// PlayFavoritesKeywords 播放收藏列表
	PlayFavoritesKeywords []string `yaml:"play_favorites_keywords"`

	// AbortXiaoAIOnPlay：handlePlay 时是否同步重启 mico_aivs_lab，杀掉小爱云端 NLP 流水线。
	// 解决"我们 player_play_url 本地歌后，小爱云端识别同一句话再返回试听版 URL 覆盖我们"的竞态。
	// 默认 true，需要时可在 config.yaml 里 commands.abort_xiaoai_on_play: false 关掉。
	AbortXiaoAIOnPlay *bool `yaml:"abort_xiaoai_on_play,omitempty"`

	// AbortHeartbeatIntervalSec：本地队列播放期间，周期性重启 mico_aivs_lab 的间隔（秒）。
	//
	// 背景：AbortXiaoAIOnPlay 只在下达播放指令那一刻重启一次 mico_aivs_lab，但它是持续运行的
	// 本地代理，重启后几秒内会自动重连小爱云端、重新同步账号在云端记录的"上次暂停内容"
	// （新闻/故事类）。经过几分钟不活动后，云端会主动把"继续播放"推给 mediaplayer——这跟
	// 我们本地队列是否还在放歌完全无关。实测表现：本地队列能正常自动切一次下一集，
	// 再往后突然就换成了很久以前听到一半的别的内容，而且中间完全没有 Idle 事件
	// （mediaplayer 内部直接切换，从未真正 Idle 过）。
	//
	// 通过在本地队列活跃期间周期性重复 AbortXiaoAI，在云端每次重新连接、还没攒够触发
	// resume 的时间窗口内就把它再打断一次，从源头上不给它机会。
	//
	// 实测记录（2026-07-31）：把间隔调到 30 秒（比默认更频繁）后，之前"自动切一次下一集，
	// 再往后被切到喜马拉雅内容"的问题就没再复现过，效果比预期的好——虽然理论上 CP 续播
	// 跟 mico_aivs_lab 是两套不同机制（见 PlayerConfig 注释），但更频繁地重启似乎确实能
	// 压低触发概率。默认值从 60 秒调到了 30 秒。
	//
	// 默认 30 秒；设为 0（显式配置指针指向 0）可关闭这个心跳。
	AbortHeartbeatIntervalSec *int `yaml:"abort_xiaoai_heartbeat_sec,omitempty"`
}

// HTTPConfig HTTP 文件服务配置
type HTTPConfig struct {
	Port    int    `yaml:"port"`
	BaseURL string `yaml:"base_url"`
}

// LXConfig LX Sync Server 在线音乐配置
type LXConfig struct {
	Enabled      bool   `yaml:"enabled"`
	BaseURL      string `yaml:"base_url"`
	FrontendAuth string `yaml:"frontend_auth"`
	UserToken    string `yaml:"user_token"`
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	Download     bool   `yaml:"download"` // true: 普通播放的在线歌曲先下载到本地再播（失败退回在线）
	DownloadDir  string `yaml:"download_dir"`
	// Source 在线搜索的平台，可写单个（"kw"）、逗号分隔（"wy,kw"）或列表（[wy, kw]）。
	// 多个时会同时搜索所有平台，合并结果打分；同样匹配的歌按这里的顺序优先。
	Source StringList `yaml:"source"`
	// Quality 音质，同样支持单个或多个（"320k,128k"）。取直链时按顺序尝试，
	// 前一个音质取不到再退到下一个。
	Quality    StringList `yaml:"quality"`
	TimeoutSec int        `yaml:"timeout_sec"`

	// Embedded 为 true 时，不走 base_url 请求外部/独立起的 LX Sync Server，
	// 而是直接在本进程内加载 pkg/lx-go 引擎（不需要手动另起一个进程/端口）。
	// 开启后 base_url/username/password/user_token/frontend_auth 都不再需要，
	// 必须配置 embedded_js_dir 指向音源脚本目录。
	Embedded bool `yaml:"embedded"`
	// EmbeddedJSDir 内嵌模式下的音源脚本目录，一般填 pkg/lx-go/js 的绝对/相对路径。
	EmbeddedJSDir string `yaml:"embedded_js_dir"`
}

// PlayerConfig 播放器行为配置，核心是"主动续播"机制，用来抢在设备原生内容续播机制之前
// 拿到"接下来该播什么"的控制权。
//
// 根因（已通过诊断快照实测确认）：设备 mediaplayer 自带一套跟 mico_aivs_lab 完全无关的
// "第三方内容提供商（CP）续播"机制——只要 mediaplayer 真正进入 idle 且没有排队的下一个
// URL，它就会去恢复你之前用原生小爱听到一半的内容（实测抓到 `player_get_context` 返回
// `audio_meta.cp.name = "ximalaya"`，即喜马拉雅）。这个空窗期出现在"我们的 track 自然
// 播完"和"我们轮询 mute_stat 检测到 Idle、再通过 RPC 往返播下一首"这段延迟之内——纯被动
// 等 Idle 事件、事后再反应，天生要跟设备本地这套机制赛跑，而本地触发通常比我们的
// "轮询 + 网络往返"更快，经常会输掉。
//
// WatchdogEnabled 开启后，Player 会用 IndexedSong.DurationMs（索引时解析 mp3 帧头真实
// 比特率算出的播放时长，不是猜的）在预估时长结束前 PreemptMarginSec 秒就主动切到下一首，
// 而不是等真的播完、等 Idle 事件、再反应——从根上不给设备的 CP 续播机制留出可乘的空窗期。
// 真正的 Idle 事件依然正常处理，两者不冲突：谁先触发，"下一首"的队列弹出就已经完成。
//
// 实测记录（2026-07-31）：4 首歌连续通过主动续播成功切歌，全程诊断快照从未再出现
// `audio_meta.cp` 字段，证明这个策略确实能稳定抢在 CP 续播之前拿到控制权。
//
// 但后续实测发现，单独把 `commands.abort_xiaoai_heartbeat_sec` 调到 30 秒（不开
// WatchdogEnabled、不开诊断）也能稳定避免这个问题复现，而且更简单、没有"牺牲每首歌
// 最后几秒内容"的代价。所以把 WatchdogEnabled 和诊断快照的默认值都改成了关闭，作为
// 可选的、更激进的兜底手段保留（主动续播这条路径本身已经过验证，需要时随时可以开）。
//
// 只对能解析出真实时长的 .mp3 生效（DurationMs>0）；其他格式/在线直链没有这个保护，
// 完全依赖 Idle 事件本身。
type PlayerConfig struct {
	// WatchdogEnabled 是否启用上述主动续播机制。默认**关闭**——实测发现单独调低
	// `commands.abort_xiaoai_heartbeat_sec` 就足够避免 CP 续播问题，不需要额外牺牲
	// 每首歌尾部几秒内容。如果心跳方案对你的设备不够用，可以再打开这个。
	WatchdogEnabled *bool `yaml:"watchdog_enabled,omitempty"`

	// PreemptMarginSec 提前多少秒（在预估时长结束前）主动切到下一首。
	// 默认 2 秒；下限 1 秒，上限不超过这首歌真实时长的 20%（避免估算误差导致砍掉太多内容）。
	PreemptMarginSec *int `yaml:"preempt_margin_sec,omitempty"`

	// DiagnosticIntervalSec：播放期间周期性打印 `player_get_context` 原始输出的间隔（秒）。
	// 纯诊断用途，不影响播放行为，用于持续观察设备端状态、排查"自动切到别的内容"这类问题
	// 是否复现。默认**关闭**（0）——已经用它定位到根因（喜马拉雅 CP 续播），日常运行不需要
	// 一直开着刷日志；需要继续排查时随时可以打开，比如设成 15 秒。
	DiagnosticIntervalSec *int `yaml:"diagnostic_interval_sec,omitempty"`

	// AnnounceEpisodeBeforePlay 是否在每次切到新一集前播报“现在播放第X集”。
	// 默认开启（true）：方便小朋友知道当前进度、中断后能知道听到第几集。
	// 设为 false 可关闭这段播报，直接播放音频内容。
	AnnounceEpisodeBeforePlay *bool `yaml:"announce_episode_before_play,omitempty"`
}

// DefaultExtensions 默认支持的音频扩展名
var DefaultExtensions = []string{".mp3", ".flac", ".wav", ".m4a", ".aac", ".ogg"}

// DefaultEpisodePattern 默认集数提取正则：匹配 第11集、11集、第11回、11 等
const DefaultEpisodePattern = `第?(\d+)[集回]?`

// DefaultCommands 默认指令关键词
var DefaultCommands = CommandsConfig{
	PlayKeywords:     []string{"播放"},
	DownloadKeywords: []string{"下载"},
	StopKeywords:     []string{"停止播放", "停止", "闭嘴", "别放了", "不要放了", "关机"},
	// 覆盖"首/个"（普通音乐语境）和"集/章/回"（故事/有声书按集播放语境）两类说法，
	// 否则用户在听故事时说"下一集"会因为 matchExact 精确匹配不上"下一首"而被当成
	// 非音乐指令直接忽略（实测过的真实 bug）。
	NextKeywords:        []string{"下一首", "下一个", "下一集", "下一章", "下一回", "下集", "换一集"},
	PreviousKeywords:    []string{"上一首", "上一个", "上一集", "上一章", "上一回", "上集"},
	RefreshKeywords:     []string{"刷新曲库"},
	RandomPlayKeywords:  []string{"随便听听"},
	RepeatOneKeywords:   []string{"单曲循环"},
	RepeatAllKeywords:   []string{"全部循环", "列表循环"},
	ShuffleModeKeywords: []string{"随机播放"},
	ContinueStoryKeywords: []string{
		"继续播放故事", "接着播放故事", "继续听故事", "接着听故事",
		"继续讲故事", "接着讲故事", "继续故事", "接着故事",
	},
	PauseKeywords:       []string{"暂停", "暂停播放", "暂停一下", "先暂停"},
	ResumeKeywords:      []string{"继续播放", "接着播放", "恢复播放"},
	ResumeShortKeywords: []string{"继续", "接着放", "继续放", "继续听", "接着听"},
	FavoriteAddKeywords: []string{
		"加入收藏", "收藏", "收藏这首", "收藏这首歌", "收藏歌曲", "我喜欢这首歌", "喜欢这首歌",
	},
	FavoriteRemoveKeywords: []string{"取消收藏", "取消收藏这首", "取消收藏这首歌", "不喜欢这首歌", "从收藏里删除"},
	PlayFavoritesKeywords: []string{
		"播放收藏", "播放我的收藏", "播放收藏歌单", "播放歌单收藏", "听收藏", "播放收藏的歌", "播放我收藏的歌",
	},
}

// ApplyDefaults 填充默认值
func (c *MusicConfig) ApplyDefaults() {
	if len(c.Extensions) == 0 {
		c.Extensions = make([]string, len(DefaultExtensions))
		copy(c.Extensions, DefaultExtensions)
	}
	if c.Search.MaxResults <= 0 {
		c.Search.MaxResults = 20
	}
	if c.Search.IndexFile == "" {
		c.Search.IndexFile = "cache/music_index.json"
	}
	if c.History.File == "" {
		c.History.File = "cache/play_history.json"
	}
	if c.History.MusicFile == "" {
		c.History.MusicFile = "cache/music_history.json"
	}
	if c.History.FavoritesFile == "" {
		c.History.FavoritesFile = "cache/favorites.json"
	}
	if len(c.LX.Source) == 0 {
		c.LX.Source = StringList{"kw"}
	}
	if len(c.LX.Quality) == 0 {
		c.LX.Quality = StringList{"128k"}
	}
	if c.LX.TimeoutSec <= 0 {
		c.LX.TimeoutSec = 10
	}
	if len(c.Commands.PlayKeywords) == 0 {
		c.Commands.PlayKeywords = make([]string, len(DefaultCommands.PlayKeywords))
		copy(c.Commands.PlayKeywords, DefaultCommands.PlayKeywords)
	}
	if len(c.Commands.DownloadKeywords) == 0 {
		c.Commands.DownloadKeywords = make([]string, len(DefaultCommands.DownloadKeywords))
		copy(c.Commands.DownloadKeywords, DefaultCommands.DownloadKeywords)
	}
	if len(c.Commands.StopKeywords) == 0 {
		c.Commands.StopKeywords = make([]string, len(DefaultCommands.StopKeywords))
		copy(c.Commands.StopKeywords, DefaultCommands.StopKeywords)
	}
	if len(c.Commands.NextKeywords) == 0 {
		c.Commands.NextKeywords = make([]string, len(DefaultCommands.NextKeywords))
		copy(c.Commands.NextKeywords, DefaultCommands.NextKeywords)
	}
	if len(c.Commands.PreviousKeywords) == 0 {
		c.Commands.PreviousKeywords = make([]string, len(DefaultCommands.PreviousKeywords))
		copy(c.Commands.PreviousKeywords, DefaultCommands.PreviousKeywords)
	}
	if len(c.Commands.RefreshKeywords) == 0 {
		c.Commands.RefreshKeywords = make([]string, len(DefaultCommands.RefreshKeywords))
		copy(c.Commands.RefreshKeywords, DefaultCommands.RefreshKeywords)
	}
	if len(c.Commands.RandomPlayKeywords) == 0 {
		c.Commands.RandomPlayKeywords = make([]string, len(DefaultCommands.RandomPlayKeywords))
		copy(c.Commands.RandomPlayKeywords, DefaultCommands.RandomPlayKeywords)
	}
	if len(c.Commands.RepeatOneKeywords) == 0 {
		c.Commands.RepeatOneKeywords = make([]string, len(DefaultCommands.RepeatOneKeywords))
		copy(c.Commands.RepeatOneKeywords, DefaultCommands.RepeatOneKeywords)
	}
	if len(c.Commands.RepeatAllKeywords) == 0 {
		c.Commands.RepeatAllKeywords = make([]string, len(DefaultCommands.RepeatAllKeywords))
		copy(c.Commands.RepeatAllKeywords, DefaultCommands.RepeatAllKeywords)
	}
	if len(c.Commands.ShuffleModeKeywords) == 0 {
		c.Commands.ShuffleModeKeywords = make([]string, len(DefaultCommands.ShuffleModeKeywords))
		copy(c.Commands.ShuffleModeKeywords, DefaultCommands.ShuffleModeKeywords)
	}
	if len(c.Commands.ContinueStoryKeywords) == 0 {
		c.Commands.ContinueStoryKeywords = make([]string, len(DefaultCommands.ContinueStoryKeywords))
		copy(c.Commands.ContinueStoryKeywords, DefaultCommands.ContinueStoryKeywords)
	}
	fill := func(dst *[]string, def []string) {
		if len(*dst) == 0 {
			*dst = append([]string(nil), def...)
		}
	}
	fill(&c.Commands.PauseKeywords, DefaultCommands.PauseKeywords)
	fill(&c.Commands.ResumeKeywords, DefaultCommands.ResumeKeywords)
	fill(&c.Commands.ResumeShortKeywords, DefaultCommands.ResumeShortKeywords)
	if c.Commands.SessionOnlyShortCommands == nil {
		t := true
		c.Commands.SessionOnlyShortCommands = &t
	}
	fill(&c.Commands.FavoriteAddKeywords, DefaultCommands.FavoriteAddKeywords)
	fill(&c.Commands.FavoriteRemoveKeywords, DefaultCommands.FavoriteRemoveKeywords)
	fill(&c.Commands.PlayFavoritesKeywords, DefaultCommands.PlayFavoritesKeywords)
	if c.Commands.AbortXiaoAIOnPlay == nil {
		// 默认开启：解决小爱云端 NLP 抢占本地播放的竞态问题
		t := true
		c.Commands.AbortXiaoAIOnPlay = &t
	}
	if c.Commands.AbortHeartbeatIntervalSec == nil {
		d := 30
		c.Commands.AbortHeartbeatIntervalSec = &d
	}
	if c.HTTP.Port <= 0 {
		c.HTTP.Port = 18080
	}
	if c.Player.WatchdogEnabled == nil {
		f := false
		c.Player.WatchdogEnabled = &f
	}
	if c.Player.PreemptMarginSec == nil {
		m := 2
		c.Player.PreemptMarginSec = &m
	}
	if c.Player.DiagnosticIntervalSec == nil {
		d := 0
		c.Player.DiagnosticIntervalSec = &d
	}
	if c.Player.AnnounceEpisodeBeforePlay == nil {
		t := true
		c.Player.AnnounceEpisodeBeforePlay = &t
	}
	for i := range c.Stories {
		if c.Stories[i].EpisodePattern == "" {
			c.Stories[i].EpisodePattern = DefaultEpisodePattern
		}
	}
}

// StringList 兼容三种 YAML 写法的字符串列表："kw"、"wy,kw"、[wy, kw]。
type StringList []string

// UnmarshalYAML 使用 yaml.v2/v3 都支持的旧式接口，避免 pkg/music 直接依赖 yaml 库。
func (l *StringList) UnmarshalYAML(unmarshal func(any) error) error {
	var list []string
	if err := unmarshal(&list); err == nil {
		*l = cleanStringList(list)
		return nil
	}
	var single string
	if err := unmarshal(&single); err != nil {
		return err
	}
	*l = cleanStringList(strings.FieldsFunc(single, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '/' || r == '|'
	}))
	return nil
}

// String 日志用。
func (l StringList) String() string { return strings.Join(l, ",") }

// First 第一个值，空列表返回 ""。
func (l StringList) First() string {
	if len(l) == 0 {
		return ""
	}
	return l[0]
}

func cleanStringList(in []string) StringList {
	out := make(StringList, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
