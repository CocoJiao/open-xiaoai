package music

import (
	"context"
	"errors"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStringListYAMLForms(t *testing.T) {
	cases := map[string][]string{
		`source: kw`:                      {"kw"},
		`source: "wy,kw"`:                 {"wy", "kw"},
		`source: [wy, tx, kw]`:            {"wy", "tx", "kw"},
		"source:\n  - WY\n  - kw\n  - wy": {"wy", "kw"},
	}
	for in, want := range cases {
		var c struct {
			Source StringList `yaml:"source"`
		}
		if err := yaml.Unmarshal([]byte(in), &c); err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if c.Source.String() != StringList(want).String() {
			t.Errorf("%q => %v want %v", in, c.Source, want)
		}
	}
}

func TestRankLXGroupsPrefersPriorityPlatformOnTie(t *testing.T) {
	wy := []lxSongInfo{{"name": "过火", "singer": "张信哲", "source": "wy"}}
	kw := []lxSongInfo{{"name": "过火", "singer": "张信哲", "source": "kw"}}
	got := rankLXGroups("过火", [][]lxSongInfo{wy, kw})
	if stringField(got[0], "source") != "wy" {
		t.Fatalf("want wy first, got %v", got[0])
	}
}

func TestRankLXGroupsBetterMatchBeatsPriority(t *testing.T) {
	wy := []lxSongInfo{{"name": "过火 (DJ版)", "singer": "某人", "source": "wy"}}
	kw := []lxSongInfo{{"name": "过火", "singer": "张信哲", "source": "kw"}}
	got := rankLXGroups("过火", [][]lxSongInfo{wy, kw})
	if stringField(got[0], "source") != "kw" {
		t.Fatalf("original on kw should beat DJ on wy, got %v", got[0])
	}
}

func TestRankLXGroupsConsensus(t *testing.T) {
	// wy 首位是冷门同名歌，kw 和 mg 都有张信哲版 → 共识版本胜出
	wy := []lxSongInfo{{"name": "过火", "singer": "路人甲", "source": "wy"}, {"name": "过火", "singer": "张信哲", "source": "wy"}}
	kw := []lxSongInfo{{"name": "过火", "singer": "张信哲", "source": "kw"}}
	got := rankLXGroups("过火", [][]lxSongInfo{wy, kw})
	if stringField(got[0], "singer") != "张信哲" || stringField(got[0], "source") != "wy" {
		t.Fatalf("want wy 张信哲 first, got %v", got[0])
	}
}

func TestSearchLXPlatformsSkipsFailedPlatform(t *testing.T) {
	groups, err := searchLXPlatforms(context.Background(), []string{"tx", "kw"}, "x",
		func(_ context.Context, p, _ string) ([]lxSongInfo, error) {
			if p == "tx" {
				return nil, errors.New("no search for tx")
			}
			return []lxSongInfo{{"name": "x"}}, nil
		})
	if err != nil || len(groups[0]) != 0 || len(groups[1]) != 1 || stringField(groups[1][0], "source") != "kw" {
		t.Fatalf("groups=%v err=%v", groups, err)
	}
	if _, err := searchLXPlatforms(context.Background(), []string{"tx"}, "x",
		func(context.Context, string, string) ([]lxSongInfo, error) { return nil, errors.New("x") }); err == nil {
		t.Fatal("all failed should error")
	}
}

func TestResolveWithQualitiesFallsBack(t *testing.T) {
	tried := []string{}
	tr, err := resolveWithQualities([]string{"320k", "128k"}, func(q string) (*LXTrack, error) {
		tried = append(tried, q)
		if q == "320k" {
			return nil, errors.New("no 320k")
		}
		return &LXTrack{URL: "u", Quality: q}, nil
	})
	if err != nil || tr.Quality != "128k" || len(tried) != 2 {
		t.Fatalf("tr=%v err=%v tried=%v", tr, err, tried)
	}
}
