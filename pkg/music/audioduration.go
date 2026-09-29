package music

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// probeAudioDuration 按文件内容（不是扩展名）识别格式并计算时长：
// FLAC 读 STREAMINFO，MP4/M4A 读 mvhd，WAV 读 fmt/data，其余按 MP3 帧头。
// 按内容识别是因为曲库里常有"扩展名是 .mp3、内容其实是 flac/m4a"的文件
// （这类文件用 mp3 帧头解析会报 no valid mp3 frame header found）。
//
// 设备（如 OH2P）的 player_get_play_status 不返回播放位置/时长，时长只能靠这里算，
// 设备状态轮询才能在歌曲结束前提前切歌、以及识别"被设备续播接管"。
func probeAudioDuration(path string, size int64) (time.Duration, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	head := make([]byte, 12)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("fLaC")):
		return probeFLAC(f)
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		return probeMP4(f, size)
	case len(head) >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "WAVE":
		return probeWAV(f)
	case bytes.HasPrefix(head, []byte("ID3")):
		// ID3 头后面也可能是 flac（少见），先按 mp3 解析
		return probeMP3Duration(path, size)
	}
	return probeMP3Duration(path, size)
}

func probeFLAC(f *os.File) (time.Duration, error) {
	// fLaC 后第一个 metadata block 必须是 STREAMINFO（34 字节）
	hdr := make([]byte, 4+34)
	if _, err := f.ReadAt(hdr, 4); err != nil {
		return 0, err
	}
	if hdr[0]&0x7f != 0 {
		return 0, errors.New("flac: first block is not STREAMINFO")
	}
	si := hdr[4:]
	sampleRate := uint64(si[10])<<12 | uint64(si[11])<<4 | uint64(si[12])>>4
	total := uint64(si[13]&0x0f)<<32 | uint64(binary.BigEndian.Uint32(si[14:18]))
	if sampleRate == 0 || total == 0 {
		return 0, errors.New("flac: unknown total samples")
	}
	return time.Duration(total * uint64(time.Second) / sampleRate), nil
}

// probeMP4 在顶层 moov 里找 mvhd（timescale + duration）。
func probeMP4(f *os.File, size int64) (time.Duration, error) {
	moov, moovSize, err := findBox(f, 0, size, "moov")
	if err != nil {
		return 0, err
	}
	mvhd, _, err := findBox(f, moov, moov+moovSize, "mvhd")
	if err != nil {
		return 0, err
	}
	buf := make([]byte, 32)
	if _, err := f.ReadAt(buf, mvhd); err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	var scale, dur uint64
	if buf[0] == 1 { // version 1: 64 位
		scale = uint64(binary.BigEndian.Uint32(buf[20:24]))
		dur = binary.BigEndian.Uint64(buf[24:32])
	} else {
		scale = uint64(binary.BigEndian.Uint32(buf[12:16]))
		dur = uint64(binary.BigEndian.Uint32(buf[16:20]))
	}
	if scale == 0 || dur == 0 {
		return 0, errors.New("mp4: empty mvhd")
	}
	return time.Duration(dur * uint64(time.Second) / scale), nil
}

// findBox 在 [start,end) 范围内找指定类型的 box，返回 box 内容起点和内容长度。
func findBox(f *os.File, start, end int64, typ string) (int64, int64, error) {
	hdr := make([]byte, 16)
	for pos := start; pos+8 <= end; {
		if _, err := f.ReadAt(hdr[:8], pos); err != nil {
			return 0, 0, err
		}
		boxSize := int64(binary.BigEndian.Uint32(hdr[:4]))
		hdrLen := int64(8)
		if boxSize == 1 {
			if _, err := f.ReadAt(hdr[8:16], pos+8); err != nil {
				return 0, 0, err
			}
			boxSize = int64(binary.BigEndian.Uint64(hdr[8:16]))
			hdrLen = 16
		} else if boxSize == 0 {
			boxSize = end - pos
		}
		if boxSize < hdrLen {
			return 0, 0, fmt.Errorf("mp4: bad box size at %d", pos)
		}
		if string(hdr[4:8]) == typ {
			return pos + hdrLen, boxSize - hdrLen, nil
		}
		pos += boxSize
	}
	return 0, 0, fmt.Errorf("mp4: box %s not found", typ)
}

func probeWAV(f *os.File) (time.Duration, error) {
	var byteRate uint32
	hdr := make([]byte, 8)
	for pos := int64(12); ; {
		if _, err := f.ReadAt(hdr, pos); err != nil {
			return 0, errors.New("wav: data chunk not found")
		}
		id, sz := string(hdr[:4]), int64(binary.LittleEndian.Uint32(hdr[4:8]))
		if id == "fmt " {
			fmtBuf := make([]byte, 16)
			if _, err := f.ReadAt(fmtBuf, pos+8); err != nil {
				return 0, err
			}
			byteRate = binary.LittleEndian.Uint32(fmtBuf[8:12])
		}
		if id == "data" {
			if byteRate == 0 {
				return 0, errors.New("wav: no byte rate")
			}
			return time.Duration(sz * int64(time.Second) / int64(byteRate)), nil
		}
		pos += 8 + sz + sz%2
	}
}

// durationProbeable 这种扩展名是否应该尝试探测时长（用于索引回填判断）。
func durationProbeable(path string) bool {
	switch strings.ToLower(pathExt(path)) {
	case ".mp3", ".flac", ".m4a", ".mp4", ".aac", ".wav":
		return true
	}
	return false
}

func pathExt(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		return p[i:]
	}
	return ""
}

// parseIntervalMs 解析 LX 搜索结果里的 interval（"05:02" / "1:02:03"）为毫秒。
func parseIntervalMs(s string) int64 {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	var total int64
	for _, p := range parts {
		var n int64
		if _, err := fmt.Sscan(p, &n); err != nil || n < 0 {
			return 0
		}
		total = total*60 + n
	}
	return total * 1000
}
