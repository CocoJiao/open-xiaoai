package lxgo

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

func TestLoadReportPrint(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	r := &loadReport{dir: "/root/js"}
	r.add("a.js", "A", loadOK, "wy[musicUrl]")
	r.add("独家音源-v6 (修复).js", "独家", loadUntested, "(无)")
	r.add("b v4.js", "B", loadDuplicate, "同名")
	r.add("x$`\".js", "X", loadFailed, "自检失败\n第二行")
	r.print()
	out := buf.String()
	for _, want := range []string{
		`"a.js"`, `rm -f "/root/js/x\$\` + "`" + `\".js" "/root/js/b v4.js"`, "❌ 不可用: (1)", "第二行",
	} {
		if want == "第二行" {
			if strings.Contains(out, want) {
				t.Errorf("detail should be first line only")
			}
			continue
		}
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"/root/js/a.js"`) {
		t.Error("ok script must not be in rm list")
	}
}

func TestToSongListUnwrapsObjects(t *testing.T) {
	item := map[string]interface{}{"name": "x"}
	for _, in := range []any{
		[]interface{}{item},
		map[string]interface{}{"list": []interface{}{item}},
		map[string]interface{}{"data": map[string]interface{}{"list": []interface{}{item}}},
	} {
		got, err := toSongList(in)
		if err != nil || len(got) != 1 {
			t.Fatalf("%v => %v %v", in, got, err)
		}
	}
}
