package lxgo

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
)

type loadStatus int

const (
	loadOK        loadStatus = iota // 自检通过
	loadUntested                    // 已加载，但脚本未上报平台，无法自检
	loadDuplicate                   // 与先加载的脚本同名，被跳过
	loadFailed                      // 读取/加载/自检失败
)

type loadEntry struct {
	file, name, detail string
	status             loadStatus
}

// loadReport 汇总 LoadSources 的结果，启动结束时打印一张公示栏，
// 并把不可用的文件用双引号包好，方便直接复制去 rm。
type loadReport struct {
	dir     string
	entries []loadEntry
}

func (r *loadReport) add(file, name string, status loadStatus, detail string) {
	r.entries = append(r.entries, loadEntry{file: file, name: name, status: status, detail: firstLine(detail)})
}

func (r *loadReport) print() {
	if r == nil || len(r.entries) == 0 {
		return
	}
	var ok, untested, dup, failed []loadEntry
	for _, e := range r.entries {
		switch e.status {
		case loadOK:
			ok = append(ok, e)
		case loadUntested:
			untested = append(untested, e)
		case loadDuplicate:
			dup = append(dup, e)
		default:
			failed = append(failed, e)
		}
	}
	var b strings.Builder
	line := strings.Repeat("=", 60)
	fmt.Fprintf(&b, "\n%s\n📋 [lx-go] 音源加载公示栏  目录: %s\n", line, r.dir)
	fmt.Fprintf(&b, "   共 %d 个脚本：✅ 可用 %d | ⚪ 未自检 %d | ⚠️ 同名跳过 %d | ❌ 不可用 %d\n%s\n",
		len(r.entries), len(ok), len(untested), len(dup), len(failed), line)
	section := func(title string, list []loadEntry) {
		if len(list) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s (%d)\n", title, len(list))
		for _, e := range list {
			fmt.Fprintf(&b, "  %s  [%s]  %s\n", shellQuote(e.file), e.name, e.detail)
		}
	}
	section("✅ 自检通过:", ok)
	section("⚪ 已加载但未上报平台、无法自检（能用但不保证）:", untested)
	section("⚠️ 同名被跳过（重复脚本）:", dup)
	section("❌ 不可用:", failed)

	bad := append(append([]loadEntry{}, failed...), dup...)
	if len(bad) > 0 {
		fmt.Fprintf(&b, "%s\n🗑️ 删除不可用 + 重复的脚本（确认后复制执行）:\n", line)
		paths := make([]string, len(bad))
		for i, e := range bad {
			paths[i] = shellQuote(filepath.Join(r.dir, e.file))
			fmt.Fprintf(&b, "  %s\n", paths[i])
		}
		fmt.Fprintf(&b, "\nrm -f %s\n", strings.Join(paths, " "))
	}
	b.WriteString(line)
	log.Print(b.String())
}

// shellQuote 双引号包裹，并转义双引号里对 shell 有特殊含义的字符。
func shellQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`")
	return `"` + r.Replace(s) + `"`
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i]) + " …"
	}
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120]) + "…"
	}
	return s
}
