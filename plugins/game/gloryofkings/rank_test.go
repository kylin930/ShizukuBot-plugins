package gloryofkings

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"shizukubot/sdk"
)

type rankMemState map[string]json.RawMessage

func (m rankMemState) Get(_ context.Context, key string) (json.RawMessage, error) { return m[key], nil }
func (m rankMemState) Set(_ context.Context, key string, v json.RawMessage) error {
	m[key] = v
	return nil
}

// rankReplies 执行一条消息并收集所有文本回复。
func rankReplies(t *testing.T, st sdk.State, group, user, message string) []string {
	t.Helper()
	replies := []string{}
	e := sdk.Event{GroupID: group, UserID: user, Message: message,
		Reply: func(_ context.Context, r sdk.Reply) error {
			replies = append(replies, r.Text)
			return nil
		}}
	if err := Handle(context.Background(), sdk.Call{Event: e, State: st}); err != nil {
		t.Fatal(err)
	}
	return replies
}

func rankJoined(replies []string) string { return strings.Join(replies, "\n") }

func fixedRankClock(t *testing.T, now time.Time) {
	t.Helper()
	old := rankNowFunc
	rankNowFunc = func() time.Time { return now }
	t.Cleanup(func() { rankNowFunc = old })
}

func seedRankState(t *testing.T, st sdk.State, now time.Time) {
	t.Helper()
	ctx := context.Background()
	user := &userState{
		IDs:     []string{"1001"},
		Current: 0,
		Names:   map[string]string{"1001": "阿狸"},
		Credentials: map[string]*credential{
			"1001": {UserID: "1001", Token: "token"},
		},
	}
	if err := sdk.SaveJSON(ctx, st, stateKey("u1"), user); err != nil {
		t.Fatal(err)
	}
	if err := saveRankIndex(ctx, st, rankIndex{"1001": "u1"}); err != nil {
		t.Fatal(err)
	}
	snapshot := &rankSnapshot{
		UpdatedAt: now.Unix(),
		Entries: map[string]rankEntry{
			"1001": {
				CampID: "1001", Owner: "u1", RoleName: "阿狸",
				RankName: "最强王者", RankStar: 10, RankSort: calcRankSort("最强王者", 10),
				PeakScore: 1650, UpdatedAt: now.Unix(),
			},
			"1002": {
				CampID: "1002", Owner: "u2", RoleName: "老玩家",
				RankName: "至尊星耀I", RankStar: 3, RankSort: calcRankSort("至尊星耀I", 3),
				PeakScore: 1500, UpdatedAt: now.Unix(),
			},
		},
	}
	if err := saveRankSnapshot(ctx, st, snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestCalcRankSort(t *testing.T) {
	cases := []struct {
		name string
		star int
		want int
	}{
		{"最强王者", 12, 7*100000 + 12},
		{"荣耀王者", 0, 7 * 100000},
		{"至尊星耀I", 3, 6*100000 + 5*1000 + 3},
		{"至尊星耀V", 1, 6*100000 + 1*1000 + 1},
		{"永恒钻石III", 0, 5*100000 + 3*1000},
		{"荣耀黄金", 2, 3*100000 + 2},
		{"", 9, 0},
		{"未知段位", 9, 0},
	}
	for _, tc := range cases {
		if got := calcRankSort(tc.name, tc.star); got != tc.want {
			t.Errorf("calcRankSort(%q,%d) = %d, want %d", tc.name, tc.star, got, tc.want)
		}
	}
}

func TestRankEntryFromProfile(t *testing.T) {
	data := &profileData{
		TargetRoleID: scalarValue("1001"),
		RoleList: []profileRole{
			{RoleID: scalarValue("1000"), RoleName: "小号"},
			{RoleID: scalarValue("1001"), RoleName: "阿狸\u0007"},
		},
		Head: profileHead{Mods: []profileMod{
			{ModID: scalarValue("701"), Name: "最强王者", Param1: `{"rankingStar":25}`},
			{ModID: scalarValue("702"), Content: "1863", Param1: `{"desc":"1863"}`},
		}},
	}
	entry := rankEntryFromProfile("1001", "u1", data, 1700000000)
	if entry.RoleName != "阿狸" || entry.RankName != "最强王者" || entry.RankStar != 25 {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.RankSort != calcRankSort("最强王者", 25) || entry.PeakScore != 1863 {
		t.Fatalf("sort/peak = %+v", entry)
	}

	empty := rankEntryFromProfile("1001", "u1", nil, 0)
	if empty.RoleName != "" || empty.RankSort != 0 || empty.PeakScore != 0 {
		t.Fatalf("nil profile = %+v", empty)
	}
}

func TestRankBotAndTotal(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.Local)
	fixedRankClock(t, now)
	st := rankMemState{}
	seedRankState(t, st, now)

	bot := rankJoined(rankReplies(t, st, "g", "u1", "#排位bot排名"))
	for _, want := range []string{"本机排位排行榜", "上榜 1 人", "1. 阿狸 - 最强王者 10星", "#排位bot排名 刷新"} {
		if !strings.Contains(bot, want) {
			t.Errorf("bot rank missing %q:\n%s", want, bot)
		}
	}
	if strings.Contains(bot, "老玩家") {
		t.Errorf("bot rank should only include indexed accounts:\n%s", bot)
	}

	total := rankJoined(rankReplies(t, st, "g", "u1", "#排位总排名"))
	for _, want := range []string{"全库排位排行榜", "上榜 2 人", "1. 阿狸 - 最强王者 10星", "2. 老玩家 - 至尊星耀I 3星", "#排位总排名 刷新"} {
		if !strings.Contains(total, want) {
			t.Errorf("total rank missing %q:\n%s", want, total)
		}
	}

	peakBot := rankJoined(rankReplies(t, st, "g", "u1", "#巅峰bot排名"))
	if !strings.Contains(peakBot, "本机巅峰分排行榜") || !strings.Contains(peakBot, "1. 阿狸 - 1650 分") {
		t.Errorf("peak bot rank = %s", peakBot)
	}

	peakTotal := rankJoined(rankReplies(t, st, "g", "u1", "#巅峰总排名"))
	for _, want := range []string{"全库巅峰分排行榜", "1. 阿狸 - 1650 分", "2. 老玩家 - 1500 分"} {
		if !strings.Contains(peakTotal, want) {
			t.Errorf("peak total rank missing %q:\n%s", want, peakTotal)
		}
	}
}

func TestRankRefreshUsesProfile(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.Local)
	fixedRankClock(t, now)
	st := rankMemState{}
	seedRankState(t, st, now)

	oldFetch := fetchRankProfile
	fetchRankProfile = func(_ context.Context, campID string, _ *campAuth) (*profileData, error) {
		return &profileData{
			RoleList: []profileRole{{RoleID: scalarValue(campID), RoleName: "阿狸"}},
			Head: profileHead{Mods: []profileMod{
				{ModID: scalarValue("701"), Name: "最强王者", Param1: `{"rankingStar":20}`},
				{ModID: scalarValue("702"), Content: "1700"},
			}},
		}, nil
	}
	t.Cleanup(func() { fetchRankProfile = oldFetch })

	replies := rankReplies(t, st, "g", "u1", "#排位bot排名 刷新")
	text := rankJoined(replies)
	for _, want := range []string{"正在更新数据，请稍候...", "1. 阿狸 - 最强王者 20星"} {
		if !strings.Contains(text, want) {
			t.Errorf("refresh missing %q:\n%s", want, text)
		}
	}

	// 刷新后的快照应被复用，不再请求营地。
	fetchRankProfile = func(context.Context, string, *campAuth) (*profileData, error) {
		t.Fatal("fresh snapshot should not fetch again")
		return nil, nil
	}
	if got := rankJoined(rankReplies(t, st, "g", "u1", "#排位bot排名")); !strings.Contains(got, "最强王者 20星") {
		t.Fatalf("cached rank = %s", got)
	}
}

func TestRankGroupUnavailable(t *testing.T) {
	st := rankMemState{}
	group := rankJoined(rankReplies(t, st, "g", "u1", "#排位排名"))
	if !strings.Contains(group, "无法获取群成员列表") || !strings.Contains(group, "#排位bot排名") {
		t.Fatalf("group rank = %s", group)
	}
	private := rankJoined(rankReplies(t, st, "", "u1", "#巅峰排名"))
	if !strings.Contains(private, "本群排名只能在群里使用") {
		t.Fatalf("private group rank = %s", private)
	}
}

func TestRankNoBindings(t *testing.T) {
	st := rankMemState{}
	got := rankJoined(rankReplies(t, st, "g", "u1", "#排位bot排名"))
	if !strings.Contains(got, "还没有人绑定营地ID") {
		t.Fatalf("empty rank = %s", got)
	}
}
