// Package genshin 通过 Enka Network 公开接口查询原神玩家的资料与角色橱窗，
// 并提供与上游一致的「原神十连」抽卡（运行时懒加载上游 Genshin.zip 素材立绘）。
package genshin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// maxBody 限制单个上游响应体大小（1 MiB），避免异常响应占用内存。
	maxBody = 1 << 20
	// storeRetry 是静态数据拉取失败后的冷却时间，避免每次查询都等满超时。
	storeRetry = 10 * time.Minute
)

var (
	// 上游地址统一放在包级变量里，测试时可替换为 httptest 服务器。
	apiURL          = "https://enka.network/api/uid/"
	characterURL    = "https://raw.githubusercontent.com/EnkaNetwork/API-docs/master/store/characters.json"
	localizationURL = "https://raw.githubusercontent.com/EnkaNetwork/API-docs/master/store/loc.json"

	client = &http.Client{Timeout: 10 * time.Second}

	// uidPattern 限定 9~10 位数字。
	uidPattern = regexp.MustCompile(`^[0-9]{9,10}$`)

	// errNotFound 对应 Enka 的 404：UID 不存在或玩家未公开角色橱窗。
	errNotFound = errors.New("UID 不存在，或玩家未公开角色橱窗（Enka 只能读取公开的角色展示）")

	// 静态数据（角色/武器名）在内存中缓存，失败后冷却一段时间再重试。
	storeMu    sync.Mutex
	store      *nameStore
	storeErrAt time.Time
)

const usage = "用法：原神 <UID> 或 原神角色 <UID>\n例如：原神 100000001\nUID 为 9~10 位数字，且玩家需在游戏中公开角色橱窗。"

// nameStore 保存角色 ID 与文本哈希到中文名的映射。
type nameStore struct {
	avatars map[string]string // avatarId -> 中文名
	texts   map[string]string // NameTextMapHash -> 中文名（武器等）
}

type playerInfo struct {
	Nickname             string `json:"nickname"`
	Level                int    `json:"level"`
	Signature            string `json:"signature"`
	WorldLevel           *int   `json:"worldLevel"`
	FinishAchievementNum int    `json:"finishAchievementNum"`
}

type propValue struct {
	IVal string `json:"ival"`
	Val  string `json:"val"`
}

type fetterInfo struct {
	ExpLevel int `json:"expLevel"`
}

type equipFlat struct {
	NameTextMapHash uint64 `json:"nameTextMapHash"`
}

type equip struct {
	ItemID int             `json:"itemId"`
	Weapon json.RawMessage `json:"weapon"`
	Flat   *equipFlat      `json:"flat"`
}

type avatarInfo struct {
	AvatarID     int                  `json:"avatarId"`
	TalentIDList []int                `json:"talentIdList"`
	PropMap      map[string]propValue `json:"propMap"`
	FetterInfo   *fetterInfo          `json:"fetterInfo"`
	EquipList    []equip              `json:"equipList"`
}

type enkaResponse struct {
	PlayerInfo     *playerInfo  `json:"playerInfo"`
	AvatarInfoList []avatarInfo `json:"avatarInfoList"`
}

// Definition 订阅「原神 <UID>」「原神角色 <UID>」查询与「原神十连」抽卡。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "genshin",
		Name:        "原神查询与抽卡",
		Version:     "1.1.0",
		Description: "发送「原神 <UID>」「原神角色 <UID>」通过 Enka Network 查询玩家资料与角色橱窗；发送「原神十连」「切换原神卡池」进行抽卡模拟（首次使用自动下载上游素材包）",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "genshin", Trigger: "原神"},
	},
		sdk.WithPermissions("state.read", "state.write"))
}

// Handle 解析指令并查询玩家信息或执行抽卡。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if sdk.MatchFull(text, "原神十连") {
		return handleGacha(ctx, c)
	}
	if sdk.MatchFull(text, "切换原神卡池") {
		return handleSwitchPool(ctx, c)
	}
	arg, ok := sdk.MatchPrefix(text, "原神角色")
	if !ok {
		arg, ok = sdk.MatchPrefix(text, "原神")
	}
	if !ok {
		return nil
	}
	if !uidPattern.MatchString(arg) {
		return c.Reply(ctx, usage)
	}
	return query(ctx, c, arg)
}

func query(ctx context.Context, c sdk.Call, uid string) error {
	info, avatars, err := fetchPlayer(ctx, uid)
	if err != nil {
		return c.Reply(ctx, "查询失败："+err.Error())
	}
	return c.Reply(ctx, render(uid, info, avatars, gameNames(ctx)))
}

// fetchPlayer 请求 Enka 的 UID 接口并解析玩家资料与角色橱窗。
func fetchPlayer(ctx context.Context, uid string) (*playerInfo, []avatarInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+uid, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, nil, errNotFound
	default:
		return nil, nil, fmt.Errorf("Enka 返回状态 %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, nil, err
	}
	var out enkaResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, nil, fmt.Errorf("解析 Enka 数据失败：%w", err)
	}
	if out.PlayerInfo == nil {
		return nil, nil, errors.New("Enka 未返回玩家资料")
	}
	return out.PlayerInfo, out.AvatarInfoList, nil
}

// render 把玩家资料与角色橱窗拼成纯文本。
func render(uid string, info *playerInfo, avatars []avatarInfo, names *nameStore) string {
	var b strings.Builder
	fmt.Fprintf(&b, "原神 UID %s\n", uid)
	fmt.Fprintf(&b, "昵称：%s\n", orNone(info.Nickname))
	fmt.Fprintf(&b, "等级：%d\n", info.Level)
	if info.WorldLevel != nil {
		fmt.Fprintf(&b, "世界等级：%d\n", *info.WorldLevel)
	}
	fmt.Fprintf(&b, "成就数：%d\n", info.FinishAchievementNum)
	fmt.Fprintf(&b, "签名：%s\n", orNone(info.Signature))
	if len(avatars) == 0 {
		b.WriteString("角色橱窗：未公开（Enka 只能读取玩家展示的角色）")
		return b.String()
	}
	fmt.Fprintf(&b, "角色橱窗（%d）：", len(avatars))
	for i, a := range avatars {
		fmt.Fprintf(&b, "\n%d. %s", i+1, avatarName(a.AvatarID, names))
		if level := avatarLevel(a); level != "" {
			b.WriteString(" Lv." + level)
		}
		if n := len(a.TalentIDList); n > 0 {
			fmt.Fprintf(&b, " %d命", n)
		}
		if a.FetterInfo != nil {
			fmt.Fprintf(&b, " 好感%d", a.FetterInfo.ExpLevel)
		}
		if weapon := weaponName(a, names); weapon != "" {
			b.WriteString(" 武器：" + weapon)
		}
	}
	return b.String()
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（无）"
	}
	return s
}

// avatarLevel 从 propMap 的 4001（等级）读取角色等级。
func avatarLevel(a avatarInfo) string {
	p, ok := a.PropMap["4001"]
	if !ok {
		return ""
	}
	if p.IVal != "" {
		return p.IVal
	}
	return p.Val
}

// avatarName 查中文名，查不到时退化为显示 avatarId。
func avatarName(id int, names *nameStore) string {
	key := strconv.Itoa(id)
	if names != nil && names.avatars[key] != "" {
		return names.avatars[key]
	}
	return "角色" + key
}

// weaponName 从装备列表里找出武器并查中文名，查不到时退化为显示 itemId。
func weaponName(a avatarInfo, names *nameStore) string {
	for _, e := range a.EquipList {
		if len(e.Weapon) == 0 || string(e.Weapon) == "null" {
			continue
		}
		if names != nil && e.Flat != nil && e.Flat.NameTextMapHash != 0 {
			if name := names.texts[strconv.FormatUint(e.Flat.NameTextMapHash, 10)]; name != "" {
				return name
			}
		}
		return "武器" + strconv.Itoa(e.ItemID)
	}
	return ""
}

// gameNames 返回内存缓存的静态数据，失败时返回 nil，调用方按 ID 降级显示。
func gameNames(ctx context.Context) *nameStore {
	storeMu.Lock()
	defer storeMu.Unlock()
	if store != nil {
		return store
	}
	if !storeErrAt.IsZero() && time.Since(storeErrAt) < storeRetry {
		return nil
	}
	s, err := fetchNameStore(ctx)
	if err != nil {
		storeErrAt = time.Now()
		return nil
	}
	store = s
	return s
}

// fetchNameStore 拉取角色 ID 与文本哈希，再与中文文本表合并。
func fetchNameStore(ctx context.Context) (*nameStore, error) {
	hashes, err := fetchCharacterHashes(ctx)
	if err != nil {
		return nil, err
	}
	texts, err := fetchChineseTexts(ctx)
	if err != nil {
		return nil, err
	}
	avatars := make(map[string]string, len(hashes))
	for id, hash := range hashes {
		if name := texts[strconv.FormatUint(hash, 10)]; name != "" {
			avatars[id] = name
		}
	}
	return &nameStore{avatars: avatars, texts: texts}, nil
}

func fetchCharacterHashes(ctx context.Context) (map[string]uint64, error) {
	body, err := fetch(ctx, characterURL)
	if err != nil {
		return nil, err
	}
	var raw map[string]struct {
		NameTextMapHash uint64 `json:"NameTextMapHash"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]uint64, len(raw))
	for id, v := range raw {
		out[id] = v.NameTextMapHash
	}
	return out, nil
}

// fetchChineseTexts 只解析 loc.json 里的 CHS 段。该文件含全部语种，整体读取会超过
// maxBody；流式解码读到 CHS 就停止，既省流量又守住单次响应 1 MiB 的上限。
func fetchChineseTexts(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, localizationURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Enka 静态数据返回状态 %s", resp.Status)
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxBody))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("loc.json 格式错误")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		if key == "CHS" {
			var texts map[string]string
			if err := dec.Decode(&texts); err != nil {
				return nil, err
			}
			return texts, nil
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("loc.json 中未找到中文文本")
}

// fetch 发起一次 GET 请求并返回限流后的响应体。
func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回状态 %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}
