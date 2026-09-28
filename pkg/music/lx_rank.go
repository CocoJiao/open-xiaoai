package music

import (
	"regexp"
	"sort"
	"strings"
)

// lxResolveMaxAttempts 按打分排序后，最多依次尝试多少个候选取直链。
// 第一名取直链失败（音源挂了/版权下架）时退到第二名，而不是直接报"找不到"。
const lxResolveMaxAttempts = 3

// lxVariantMarkers 常见"非原版"标记。用户没有明确说这些词时，带这些标记的结果降权，
// 避免"播放晴天"命中"晴天（伴奏）"、"晴天 DJ 版"、"翻唱晴天"。
var lxVariantMarkers = []string{
	"伴奏", "纯音乐", "铃声", "片段", "翻唱", "翻自", "cover", "dj", "remix",
	"live", "现场", "karaoke", "instrumental", "伴唱", "试听", "加速", "降调", "升调", "0.8x", "1.2x",
}

var lxSingerSplitRe = regexp.MustCompile(`[、/&,，;；]|\s+feat\.?\s+|\s+ft\.?\s+`)

// lxNormalize 打分用的规范化：小写、去空白和常见标点。
func lxNormalize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '\t', '·', '-', '_', '.', '。', '，', ',', '、', '\'', '"', '“', '”', '《', '》', '(', ')', '（', '）', '[', ']', '【', '】':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// lxSplitSingers 把 "周杰伦、费玉清" 这类多歌手字段拆开。
func lxSplitSingers(singer string) []string {
	parts := lxSingerSplitRe.Split(singer, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if n := lxNormalize(p); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// lxBaseName 去掉歌名里括号部分（"晴天 (Live)" → "晴天"），用于和关键词做精确比对。
var lxBracketRe = regexp.MustCompile(`[\(（\[【].*?[\)）\]】]`)

func lxBaseName(name string) string {
	return lxNormalize(lxBracketRe.ReplaceAllString(name, ""))
}

// scoreLXCandidate 给一个在线搜索结果打分，分数越高越符合用户说的话。
//
// 关键词没有结构（用户说"周杰伦晴天"/"晴天周杰伦"/"周杰伦的晴天"/"晴天"），所以不去猜哪部分
// 是歌手哪部分是歌名，而是看候选的歌名/歌手是否能"拼出"关键词：
//   - 关键词 == 歌名：高分
//   - 关键词 == 歌手+歌名 / 歌名+歌手（中间允许一个"的"）：最高分
//   - 关键词同时包含歌名和某个歌手：中高分
//   - 只包含歌名：中分（歌名越长越可信）
//   - 只包含歌手：低分（适用于"播放周杰伦"这种只说歌手的情况）
//
// 此外对用户没说的"伴奏/DJ/Live/翻唱"等版本降权，搜索结果原始排名作为很小的并列打破项。
func scoreLXCandidate(keyword, name, singer string, rank int) int {
	kw := lxNormalize(keyword)
	if kw == "" {
		return 0
	}
	kwNoDe := strings.ReplaceAll(kw, "的", "")
	fullName := lxNormalize(name)
	base := lxBaseName(name)
	singers := lxSplitSingers(singer)

	score := 0
	switch {
	case base != "" && (kw == base || kw == fullName):
		score += 100
	case base != "" && strings.Contains(kw, base):
		// 歌名是关键词的一部分；歌名越长越不可能是巧合
		score += 40 + min(len([]rune(base))*5, 30)
	case base != "" && strings.Contains(base, kw):
		// 关键词只是歌名的一部分（"播放稻"→"稻香"）
		score += 20
	}

	singerHit := false
	for _, s := range singers {
		if s == "" {
			continue
		}
		if strings.Contains(kw, s) {
			singerHit = true
			if base != "" {
				for _, combo := range []string{s + base, base + s, s + "的" + base} {
					if kw == combo || kwNoDe == strings.ReplaceAll(combo, "的", "") {
						score += 150
						break
					}
				}
			}
			break
		}
	}
	if singerHit {
		score += 30
		if base != "" && strings.Contains(kw, base) {
			score += 20
		}
	}

	for _, marker := range lxVariantMarkers {
		if strings.Contains(fullName, marker) && !strings.Contains(kw, marker) {
			score -= 40
			break
		}
	}
	score -= rank
	return score
}

// rankLXSongs 按 scoreLXCandidate 重新排序，稳定排序保证同分时保留音源原始顺序。
func rankLXSongs(keyword string, songs []lxSongInfo) []lxSongInfo {
	type scored struct {
		song  lxSongInfo
		score int
	}
	list := make([]scored, len(songs))
	for i, s := range songs {
		list[i] = scored{song: s, score: scoreLXCandidate(keyword, stringField(s, "name"), stringField(s, "singer"), i)}
	}
	sort.SliceStable(list, func(a, b int) bool { return list[a].score > list[b].score })
	out := make([]lxSongInfo, len(list))
	for i, s := range list {
		out[i] = s.song
	}
	return out
}

// filterLXSongsBySinger 用于"XX的歌"：只保留歌手字段真的包含 artist 的结果，
// 同名歌曲去重（不同音源/版本常会重复出现），并跳过伴奏/DJ 等非原版。
func filterLXSongsBySinger(artist string, songs []lxSongInfo, limit int) []lxSongInfo {
	a := lxNormalize(artist)
	if a == "" {
		return nil
	}
	seen := map[string]bool{}
	out := make([]lxSongInfo, 0, limit)
	for _, s := range songs {
		hit := false
		for _, sg := range lxSplitSingers(stringField(s, "singer")) {
			if sg == a || strings.Contains(sg, a) || strings.Contains(a, sg) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		name := lxNormalize(stringField(s, "name"))
		variant := false
		for _, marker := range lxVariantMarkers {
			if strings.Contains(name, marker) {
				variant = true
				break
			}
		}
		base := lxBaseName(stringField(s, "name"))
		if variant || base == "" || seen[base] {
			continue
		}
		seen[base] = true
		out = append(out, s)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}
