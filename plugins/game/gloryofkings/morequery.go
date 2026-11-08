package gloryofkings

// 补齐 P1 遗留的三件小事：
//  1. #王者添加tk —— 手工导入 token（上游 accountManager 的 addtk 分支）
//  2. #查<英雄>战绩 / #查战绩 <英雄> —— 按英雄筛最近战绩
//  3. 未绑定时的营地绑定教程图（上游 resources/img/营地ID获取.png）

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"shizukubot/sdk"
)

var (
	// #王者添加tk <营地ID> <token>，兼容大小写与空格。
	addTokenRe = regexp.MustCompile(`(?i)^(?:王者)?添加\s*tk\s+(\d{5,})\s+([0-9A-Za-z_\-+/=]{16,})$`)
	// 指令前缀单拎出来，参数不全时回用法提示。
	addTokenPrefixRe = regexp.MustCompile(`(?i)^(?:王者)?添加\s*tk\b`)
	// #查妲己战绩 / #查妲己排位战绩；「查询…」交给战绩列表规则，避免互相抢。
	heroBattleRe = regexp.MustCompile(`^查(.+?)(排位|巅峰)?战绩$`)
	// 英雄头像 URL 里的英雄 ID（/heroId00.jpg）。
	heroIconIDRe = regexp.MustCompile(`(\d+)00\.jpg`)

	bindGuideOnce sync.Once
	bindGuideData []byte
)

const addTokenUsage = "用法：#王者添加tk <营地ID> <token>（仅私聊）\n示例：#王者添加tk 123456 xxxxxxxxxxxxxxxx"

// bindGuideImage 返回内嵌的绑定教程图；读取失败时返回 nil，调用方退回纯文字。
func bindGuideImage() []byte {
	bindGuideOnce.Do(func() {
		if data, err := assets.ReadFile("resources/img/bind_guide.png"); err == nil {
			bindGuideData = data
		}
	})
	return bindGuideData
}

// bindGuideKeyboard 未绑定提示的快捷按钮。按钮只是把指令填进输入框，
// 真正的逻辑仍走文本指令。
func bindGuideKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(
		sdk.CommandButton("gok-help", "查看指令", "#王者帮助"),
		sdk.CommandButton("gok-query", "查询战绩", "#查询战绩"),
		sdk.CommandButton("gok-sign", "营地签到", "#王者签到"),
	))
}

// campKeyboard 战绩/签到/兑换结果下方的快捷按钮。
func campKeyboard() sdk.Keyboard {
	return sdk.NewKeyboard(sdk.NewRow(
		sdk.CommandButton("gok-query", "查询战绩", "#查询战绩"),
		sdk.CommandButton("gok-sign", "营地签到", "#王者签到"),
		sdk.CommandButton("gok-exchange", "营地兑换", "#营地兑换"),
	))
}

// sendBindGuide 发未绑定提示 + 教程图。QQ 开通 Markdown/按钮权限时附带按钮，
// 发送失败则退回普通文本，教程图始终发送。
func sendBindGuide(ctx context.Context, c sdk.Call, text string) error {
	message := campIDGuide
	if text != "" {
		message = text + "\n" + campIDGuide
	}
	if err := c.ReplyKeyboard(ctx, message, bindGuideKeyboard()); err != nil {
		if err := c.Reply(ctx, message); err != nil {
			return err
		}
	}
	if image := bindGuideImage(); len(image) > 0 {
		return c.ReplyImageBytes(ctx, image)
	}
	return nil
}

// isAddTokenCommand 判断是不是「添加tk」指令（参数不全时回用法）。
func isAddTokenCommand(body string) bool { return addTokenPrefixRe.MatchString(body) }

// parseAddToken 解析 #王者添加tk。
func parseAddToken(body string) (campID, token string, ok bool) {
	matched := addTokenRe.FindStringSubmatch(body)
	if matched == nil {
		return "", "", false
	}
	return matched[1], matched[2], true
}

// addTokenCommand 校验 token 后写进用户状态。token 属于敏感信息，只允许私聊。
func addTokenCommand(ctx context.Context, c sdk.Call, campID, token string) error {
	if c.Event.GroupID != "" {
		return c.Reply(ctx, "token 属于账号凭证，请私聊我发：#王者添加tk <营地ID> <token>")
	}

	client := newCampClient()
	auth := &campAuth{UserID: campID, Token: token}
	code, msg, _, _, err := client.callRaw(ctx, "/user/getcampfriends", map[string]any{}, auth)
	if err != nil {
		return c.Reply(ctx, "校验 token 失败："+err.Error())
	}
	if code != 0 {
		switch code {
		case -10001, -10002, -30003:
			return c.Reply(ctx, "token 无效或已过期，请重新抓包后再试")
		default:
			return c.Reply(ctx, "营地没有接受这个 token："+businessError(code, msg).Error())
		}
	}

	state := loadUserState(ctx, c.State, c.Event.UserID)
	state.putCredential(&credential{
		UserID:   campID,
		Token:    token,
		Platform: "app",
		Nickname: state.Names[campID],
		SavedAt:  time.Now().Unix(),
	})
	index := state.bindID(campID)
	if name := refreshRoleName(ctx, client, state, campID); name != "" {
		state.Names[campID] = name
	}
	if err := saveUserState(ctx, c.State, c.Event.UserID, state); err != nil {
		return c.Reply(ctx, "token 校验通过，但保存失败："+err.Error())
	}

	label := accountLabel(state, campID)
	return c.Reply(ctx, fmt.Sprintf("已添加并切换到营地账号 %s（第 %d 个）\n发送 #查询战绩 就能用了", label, index+1))
}

// parseHeroBattle 解析英雄战绩指令。
// 支持 #查妲己战绩 / #查妲己排位战绩 / #查战绩 妲己。
func parseHeroBattle(body string) (hero string, option int, ok bool) {
	if rest, cut := cutCommand(body, "查战绩"); cut {
		if rest != "" {
			return rest, 0, true
		}
		return "", 0, false
	}
	matched := heroBattleRe.FindStringSubmatch(body)
	if matched == nil {
		return "", 0, false
	}
	hero = strings.TrimSpace(matched[1])
	if hero == "" || strings.HasPrefix(hero, "询") {
		return "", 0, false
	}
	return hero, modeOption(matched[2]), true
}

// resolveHeroID 用官网英雄列表把中文名解析成 heroId，口径与 #查皮肤 一致。
func resolveHeroID(ctx context.Context, input string) (heroID, heroName string, err error) {
	heroes, err := fetchPvpHeroList(ctx)
	if err != nil {
		return "", "", err
	}
	want := normalizeHeroName(expandYuanAbbr(strings.TrimSpace(input)))
	if want == "" {
		return "", "", errors.New("英雄名不能为空")
	}
	var fuzzy []pvpHero
	for _, hero := range heroes {
		name := normalizeHeroName(hero.CName)
		if name == want {
			return hero.EName.String(), hero.CName, nil
		}
		if strings.Contains(name, want) {
			fuzzy = append(fuzzy, hero)
		}
	}
	switch len(fuzzy) {
	case 0:
		return "", "", fmt.Errorf("没找到叫「%s」的英雄", input)
	case 1:
		return fuzzy[0].EName.String(), fuzzy[0].CName, nil
	default:
		names := make([]string, 0, len(fuzzy))
		for _, hero := range fuzzy {
			names = append(names, hero.CName)
		}
		return "", "", fmt.Errorf("「%s」匹配到多个英雄：%s，写全名再试", input, strings.Join(names, "、"))
	}
}

// heroOwnedBattle 判断一场战绩是不是这个英雄打的。
func heroOwnedBattle(item battleItem, heroID string) bool {
	if got := strings.TrimSpace(item.HeroID.String()); got != "" {
		return got == heroID
	}
	matched := heroIconIDRe.FindStringSubmatch(item.HeroIcon)
	return matched != nil && matched[1] == heroID
}

// heroBattleCommand 拉战绩列表，按英雄筛出来再出图。
// 最多翻 3 页（营地一页 10 场），够 12 场就停，避免冷门英雄把人打爆。
func heroBattleCommand(ctx context.Context, c sdk.Call, heroInput string, option int) error {
	state := loadUserState(ctx, c.State, c.Event.UserID)
	if len(state.IDs) == 0 {
		return sendBindGuide(ctx, c, "你还没有绑定营地ID")
	}
	id := state.currentID()
	cred := state.bestCredential(id)
	if cred == nil {
		return c.Reply(ctx, noCredentialHint)
	}

	heroID, heroName, err := resolveHeroID(ctx, heroInput)
	if err != nil {
		return c.Reply(ctx, err.Error())
	}

	client := newCampClient()
	auth := cred.auth()
	var picked []battleItem
	var last *battleListData
	lastTime := "0"
	for page := 0; page < 3 && len(picked) < 12; page++ {
		data, err := client.getMoreBattleListPage(ctx, id, auth, option, lastTime)
		if err != nil {
			if errors.Is(err, ErrAuthInvalid) {
				return c.Reply(ctx, "营地登录态已失效，请重新发送 #营地wx全局登录 或 #营地QQ全局登录")
			}
			if errors.Is(err, ErrRateLimited) || errors.Is(err, ErrProfileHidden) {
				return c.Reply(ctx, err.Error())
			}
			return c.Reply(ctx, "查询失败："+err.Error())
		}
		last = data
		for _, item := range data.List {
			if heroOwnedBattle(item, heroID) {
				picked = append(picked, item)
			}
		}
		if !data.HasMore || len(data.List) == 0 || data.LastTime.String() == lastTime {
			break
		}
		lastTime = data.LastTime.String()
	}

	roleJob := ""
	if last != nil {
		roleJob = last.RoleJobName
	}
	view := buildBattleView(&battleListData{List: picked}, "", roleJob, option)
	view.HeroLabel = heroName
	if view.EmptyState {
		view.EmptyTitle = "最近没有这个英雄的战绩"
		view.EmptyDescription = fmt.Sprintf("ID: %s 最近查到的对局里没有「%s」", id, heroName)
	}

	image, renderErr := renderBattleList(ctx, view)
	if renderErr != nil {
		if len(picked) == 0 {
			return c.Reply(ctx, fmt.Sprintf("营地ID %s 最近没有「%s」的战绩", id, heroName))
		}
		return c.Reply(ctx, fmt.Sprintf("营地ID %s 的「%s」战绩：\n%s", id, heroName, battleSummary(&battleListData{List: picked}, id)))
	}
	return c.ReplyImageBytes(ctx, image)
}
