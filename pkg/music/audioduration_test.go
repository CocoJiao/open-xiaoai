package music

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProbeFLACByContentEvenWithMP3Ext(t *testing.T) {
	// 扩展名 .mp3，内容其实是 flac：44100Hz，441000 个采样 = 10s
	si := make([]byte, 34)
	sr, total := uint64(44100), uint64(441000)
	si[10] = byte(sr >> 12)
	si[11] = byte(sr >> 4)
	si[12] = byte(sr<<4) | 0x02
	si[13] = byte(total>>32) & 0x0f
	binary.BigEndian.PutUint32(si[14:18], uint32(total))
	data := append([]byte("fLaC"), 0x00, 0, 0, 34)
	data = append(data, si...)
	p := filepath.Join(t.TempDir(), "fake.mp3")
	_ = os.WriteFile(p, data, 0644)
	d, err := probeAudioDuration(p, int64(len(data)))
	if err != nil || d != 10*time.Second {
		t.Fatalf("d=%v err=%v", d, err)
	}
}

func TestProbeMP4(t *testing.T) {
	box := func(typ string, body []byte) []byte {
		b := make([]byte, 8)
		binary.BigEndian.PutUint32(b, uint32(8+len(body)))
		copy(b[4:], typ)
		return append(b, body...)
	}
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:16], 1000)   // timescale
	binary.BigEndian.PutUint32(mvhd[16:20], 245000) // 245s
	data := append(box("ftyp", []byte("M4A \x00\x00\x00\x00")), box("free", make([]byte, 5))...)
	data = append(data, box("moov", box("mvhd", mvhd))...)
	p := filepath.Join(t.TempDir(), "a.m4a")
	_ = os.WriteFile(p, data, 0644)
	d, err := probeAudioDuration(p, int64(len(data)))
	if err != nil || d != 245*time.Second {
		t.Fatalf("d=%v err=%v", d, err)
	}
}

func TestParseIntervalMs(t *testing.T) {
	for in, want := range map[string]int64{"05:02": 302000, "1:02:03": 3723000, "": 0, "abc": 0, "5": 0} {
		if got := parseIntervalMs(in); got != want {
			t.Errorf("%q=%d want %d", in, got, want)
		}
	}
}
