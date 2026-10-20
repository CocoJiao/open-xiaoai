package lxgo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 原生搜索：直接调用各平台官方搜索接口，不依赖用户脚本，也不依赖第三方聚合站。
// 用户脚本通常只实现 musicUrl，只负责"拿到 songInfo 之后解析直链"。
// 搜索优先级：原生搜索 > 脚本自带的 search。

const nativeSearchSource = "原生搜索"

// nativeSearchEnabled 仅供测试关闭原生搜索（避免单测访问真实网络）。
var nativeSearchEnabled = true

var (
	nativeHTTPClient = &http.Client{Timeout: 10 * time.Second}
	htmlTagRe        = regexp.MustCompile(`<[^>]*>`)
)

// nativeSearchSupports 是否有对应平台的原生搜索实现。
func nativeSearchSupports(platform string) bool {
	if !nativeSearchEnabled {
		return false
	}
	switch platform {
	case "wy", "tx", "kw", "kg", "mg":
		return true
	}
	return false
}

// nativeSearch 返回 lx 风格的 songInfo 列表（含 name/singer/source/songmid/interval 等）。
func nativeSearch(ctx context.Context, platform, keyword string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	var (
		list []map[string]any
		err  error
	)
	switch platform {
	case "wy":
		list, err = searchWY(ctx, keyword, limit)
	case "tx":
		list, err = searchTX(ctx, keyword, limit)
	case "kw":
		list, err = searchKW(ctx, keyword, limit)
	case "kg":
		list, err = searchKG(ctx, keyword, limit)
	case "mg":
		list, err = searchMG(ctx, keyword, limit)
	default:
		return nil, fmt.Errorf("native search does not support platform %q", platform)
	}
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("empty result")
	}
	if len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

func nativeDo(ctx context.Context, method, u string, body []byte, headers map[string]string, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := nativeHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	return nil
}

func cleanText(s string) string {
	return strings.TrimSpace(html.UnescapeString(htmlTagRe.ReplaceAllString(s, "")))
}

func fmtInterval(sec int) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%02d:%02d", sec/60, sec%60)
}

func anyStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatInt(int64(t), 10)
	case json.Number:
		return t.String()
	}
	return ""
}

// ---- 网易云 ----
func searchWY(ctx context.Context, kw string, limit int) ([]map[string]any, error) {
	q := url.Values{}
	q.Set("s", kw)
	q.Set("type", "1")
	q.Set("offset", "0")
	q.Set("total", "true")
	q.Set("limit", strconv.Itoa(limit))
	var resp struct {
		Code   int `json:"code"`
		Result struct {
			Songs []struct {
				ID      int64  `json:"id"`
				Name    string `json:"name"`
				Dur     int    `json:"duration"`
				Artists []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album struct {
					Name string `json:"name"`
				} `json:"album"`
			} `json:"songs"`
		} `json:"result"`
	}
	err := nativeDo(ctx, "GET", "https://music.163.com/api/search/get/web?"+q.Encode(), nil,
		map[string]string{"Referer": "https://music.163.com/"}, &resp)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(resp.Result.Songs))
	for _, s := range resp.Result.Songs {
		names := make([]string, 0, len(s.Artists))
		for _, a := range s.Artists {
			names = append(names, a.Name)
		}
		id := strconv.FormatInt(s.ID, 10)
		out = append(out, map[string]any{
			"name": s.Name, "singer": strings.Join(names, "、"), "albumName": s.Album.Name,
			"source": "wy", "id": id, "songmid": id, "interval": fmtInterval(s.Dur / 1000),
			"meta": map[string]any{"wy": map[string]any{"id": id, "url_id": id, "lyric_id": id}},
		})
	}
	return out, nil
}

// ---- QQ 音乐 ----
func searchTX(ctx context.Context, kw string, limit int) ([]map[string]any, error) {
	payload, _ := json.Marshal(map[string]any{
		"comm": map[string]any{"ct": "19", "cv": "1859", "uin": "0"},
		"req": map[string]any{
			"method": "DoSearchForQQMusicDesktop",
			"module": "music.search.SearchCgiService",
			"param":  map[string]any{"query": kw, "num_per_page": limit, "page_num": 1, "search_type": 0},
		},
	})
	var resp struct {
		Req struct {
			Data struct {
				Body struct {
					Song struct {
						List []struct {
							ID       int64  `json:"id"`
							Mid      string `json:"mid"`
							Name     string `json:"name"`
							Interval int    `json:"interval"`
							Singer   []struct {
								Name string `json:"name"`
							} `json:"singer"`
							Album struct {
								Name string `json:"name"`
								Mid  string `json:"mid"`
							} `json:"album"`
							File struct {
								MediaMid string `json:"media_mid"`
							} `json:"file"`
						} `json:"list"`
					} `json:"song"`
				} `json:"body"`
			} `json:"data"`
		} `json:"req"`
	}
	err := nativeDo(ctx, "POST", "https://u.y.qq.com/cgi-bin/musicu.fcg", payload,
		map[string]string{"Content-Type": "application/json", "Referer": "https://y.qq.com/"}, &resp)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, s := range resp.Req.Data.Body.Song.List {
		names := make([]string, 0, len(s.Singer))
		for _, a := range s.Singer {
			names = append(names, a.Name)
		}
		songID := strconv.FormatInt(s.ID, 10)
		out = append(out, map[string]any{
			"name": s.Name, "singer": strings.Join(names, "、"), "albumName": s.Album.Name,
			"source": "tx", "id": s.Mid, "songmid": s.Mid, "songId": songID, "strMediaMid": s.File.MediaMid,
			"albumMid": s.Album.Mid, "interval": fmtInterval(s.Interval),
			"meta": map[string]any{"tx": map[string]any{"id": s.Mid, "songId": songID}},
		})
	}
	return out, nil
}

// ---- 酷我 ----
func searchKW(ctx context.Context, kw string, limit int) ([]map[string]any, error) {
	q := url.Values{}
	q.Set("all", kw)
	q.Set("pn", "0")
	q.Set("rn", strconv.Itoa(limit))
	for k, v := range map[string]string{
		"client": "kt", "ft": "music", "cluster": "0", "strategy": "2012", "encoding": "utf8",
		"rformat": "json", "vermerge": "1", "mobi": "1", "issubtitle": "1", "show_copyright_off": "1",
		"newver": "1", "vipver": "1",
	} {
		q.Set(k, v)
	}
	var resp struct {
		AbsList []struct {
			RID      string `json:"MUSICRID"`
			Name     string `json:"SONGNAME"`
			Artist   string `json:"ARTIST"`
			Album    string `json:"ALBUM"`
			Duration string `json:"DURATION"`
			Pic      string `json:"web_albumpic_short"`
		} `json:"abslist"`
	}
	err := nativeDo(ctx, "GET", "http://search.kuwo.cn/r.s?"+q.Encode(), nil, nil, &resp)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, s := range resp.AbsList {
		mid := strings.TrimPrefix(s.RID, "MUSIC_")
		if mid == "" {
			continue
		}
		dur, _ := strconv.Atoi(s.Duration)
		out = append(out, map[string]any{
			"name": cleanText(s.Name), "singer": strings.ReplaceAll(cleanText(s.Artist), "&", "、"),
			"albumName": cleanText(s.Album), "source": "kw", "id": mid, "songmid": mid,
			"interval": fmtInterval(dur),
			"meta":     map[string]any{"kw": map[string]any{"id": mid}},
		})
	}
	return out, nil
}

// ---- 酷狗 ----
func searchKG(ctx context.Context, kw string, limit int) ([]map[string]any, error) {
	q := url.Values{}
	q.Set("keyword", kw)
	q.Set("page", "1")
	q.Set("pagesize", strconv.Itoa(limit))
	q.Set("userid", "0")
	q.Set("platform", "WebFilter")
	q.Set("filter", "2")
	q.Set("iscorrection", "1")
	q.Set("privilege_filter", "0")
	q.Set("area_code", "1")
	var resp struct {
		Data struct {
			Lists []struct {
				Name     string `json:"SongName"`
				Singer   string `json:"SingerName"`
				Album    string `json:"AlbumName"`
				Duration int    `json:"Duration"`
				FileHash string `json:"FileHash"`
				AlbumID  any    `json:"AlbumID"`
				EMixSong any    `json:"EMixSongID"`
				HQ       string `json:"HQFileHash"`
				SQ       string `json:"SQFileHash"`
			} `json:"lists"`
		} `json:"data"`
	}
	err := nativeDo(ctx, "GET", "https://songsearch.kugou.com/song_search_v2?"+q.Encode(), nil, nil, &resp)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, s := range resp.Data.Lists {
		if s.FileHash == "" {
			continue
		}
		out = append(out, map[string]any{
			"name": cleanText(s.Name), "singer": strings.ReplaceAll(cleanText(s.Singer), "、", "、"),
			"albumName": cleanText(s.Album), "source": "kg", "id": s.FileHash, "songmid": s.FileHash,
			"hash": s.FileHash, "albumId": anyStr(s.AlbumID), "interval": fmtInterval(s.Duration),
			"meta": map[string]any{"kg": map[string]any{"hash": s.FileHash, "albumId": anyStr(s.AlbumID), "id": anyStr(s.EMixSong)}},
		})
	}
	return out, nil
}

// ---- 咪咕 ----
func searchMG(ctx context.Context, kw string, limit int) ([]map[string]any, error) {
	q := url.Values{}
	q.Set("text", kw)
	q.Set("pageNo", "1")
	q.Set("pageSize", strconv.Itoa(limit))
	q.Set("searchSwitch", `{"song":1}`)
	var resp struct {
		Data struct {
			Result []struct {
				ID          string `json:"id"`
				ContentID   string `json:"contentId"`
				CopyrightID string `json:"copyrightId"`
				Name        string `json:"name"`
				Singers     []struct {
					Name string `json:"name"`
				} `json:"singers"`
				Albums []struct {
					Name string `json:"name"`
				} `json:"albums"`
				ImgItems []struct {
					Img string `json:"img"`
				} `json:"imgItems"`
			} `json:"result"`
		} `json:"songResultData"`
	}
	err := nativeDo(ctx, "GET", "https://pd.musicapp.migu.cn/MIGUM3.0/v1.0/content/search_all.do?"+q.Encode(), nil,
		map[string]string{
			"Referer":    "https://m.music.migu.cn/",
			"User-Agent": "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15",
		}, &resp)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, s := range resp.Data.Result {
		if s.ID == "" {
			continue
		}
		names := make([]string, 0, len(s.Singers))
		for _, a := range s.Singers {
			names = append(names, a.Name)
		}
		album := ""
		if len(s.Albums) > 0 {
			album = s.Albums[0].Name
		}
		img := ""
		if len(s.ImgItems) > 0 {
			img = s.ImgItems[0].Img
		}
		out = append(out, map[string]any{
			"name": s.Name, "singer": strings.Join(names, "、"), "albumName": album,
			"source": "mg", "id": s.ID, "songmid": s.ID, "copyrightId": s.CopyrightID,
			"contentId": s.ContentID, "img": img, "interval": "00:00",
			"meta": map[string]any{"mg": map[string]any{"id": s.ID, "copyrightId": s.CopyrightID, "contentId": s.ContentID}},
		})
	}
	return out, nil
}
