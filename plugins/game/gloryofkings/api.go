package gloryofkings

// 王者营地接口的最小可用客户端：只覆盖 Phase 1 需要的
// 登录态校验、角色信息与战绩列表。请求签名、加密与响应解密
// 与上游 GloryOfKings-Plugin（MIT, cchanlan）的 utils/api.js 保持一致。

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	campMainURL      = "https://kohcamp.qq.com"
	campMainHost     = "kohcamp.qq.com"
	campGameHost     = "ssl.kohsocialapp.qq.com"
	defaultPublicKey = "MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQC0h62mV/zjJtFsNdfFNlxksfUOpjDI2KCcBrPiA8T7szABT4InLDTrdXAW84QyGNiazB0i7pgPCNGSAYbiJrCRutZ5jQsVS0Wg/RnXfwVQDJcAHJDjP5IXyroeLX7NUxDai8nPcpfRsvq6sneobyPexZSH0TlVSnecsJZTj5wu/wIDAQAB"
	requestTimeout   = 10 * time.Second
)

var (
	// ErrAuthInvalid 登录态被营地判定失效，需要重新扫码。
	ErrAuthInvalid = errors.New("营地登录态已失效，请重新扫码登录")
	// ErrRateLimited 营地频控。上游口径是命中一次静默 12 小时。
	ErrRateLimited = errors.New("营地接口触发频控，请稍后再试（频繁查询会被静默 12 小时）")
	// ErrProfileHidden 对方把战绩藏起来了。
	ErrProfileHidden = errors.New("对方已隐藏战绩，无法查询")
	// ErrSeasonHidden 对方把赛季表现藏起来了（-30408 seasonpage / -10110 getfightdata）。
	ErrSeasonHidden = errors.New("对方已隐藏赛季表现，无法查询")
	// ErrNotFound 营地没有返回数据。
	ErrNotFound = errors.New("营地没有返回数据")
)

type campError struct {
	code int
	msg  string
}

func (e *campError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return fmt.Sprintf("营地接口返回错误码 %d", e.code)
}

// campAuth 是一次请求需要的鉴权与设备参数，字段默认值与上游 buildConfig 对齐。
// 账号自己的值优先，缺了才落到默认值。
type campAuth struct {
	UserID       string
	Token        string
	UserKey      string
	EncodeRes    string
	OpenID       string
	GameOpenID   string
	GameRoleID   string
	GameServerID string
	PublicKey    string

	GameAreaID     string
	GameUserSex    string
	KohDimGender   string
	UserAgent      string
	XClientProto   string
	ContentEncrypt string
	AcceptEncrypt  string
	NoEncrypt      string
	IsTrpcRequest  string
	CChannelID     string
	CClientVerCode string
	CClientVerName string
	CCurrentGameID string
	CGameID        string
	CGzip          string
	CIsArm64       string
	CSupportArm64  string
	CSystem        string
	CSysVerCode    string
	CSysVerName    string
	CPUHardware    string
	TinkerID       string

	ServerTimeOffsetMs int64
	XLogUID            string
	Traceparent        string
}

func (a *campAuth) applyDefaults() {
	putIfEmpty := func(dst *string, value string) {
		if *dst == "" {
			*dst = value
		}
	}
	putIfEmpty(&a.PublicKey, defaultPublicKey)
	putIfEmpty(&a.GameAreaID, "1")
	putIfEmpty(&a.GameUserSex, "1")
	putIfEmpty(&a.KohDimGender, "2")
	putIfEmpty(&a.UserAgent, "okhttp/4.9.1")
	putIfEmpty(&a.XClientProto, "https")
	putIfEmpty(&a.NoEncrypt, "1")
	putIfEmpty(&a.IsTrpcRequest, "true")
	putIfEmpty(&a.CChannelID, "10003391")
	putIfEmpty(&a.CClientVerCode, "2057957801")
	putIfEmpty(&a.CClientVerName, "10.111.0323")
	putIfEmpty(&a.CCurrentGameID, "20001")
	putIfEmpty(&a.CGameID, "20001")
	putIfEmpty(&a.CGzip, "1")
	putIfEmpty(&a.CIsArm64, "true")
	putIfEmpty(&a.CSupportArm64, "true")
	putIfEmpty(&a.CSystem, "android")
	putIfEmpty(&a.CSysVerCode, "34")
	putIfEmpty(&a.CSysVerName, "14")
	putIfEmpty(&a.CPUHardware, "qcom")
	putIfEmpty(&a.TinkerID, "2057957801_64_0")
}

type campClient struct {
	http    *http.Client
	xLogUID string
}

func newCampClient() *campClient {
	return &campClient{
		http:    &http.Client{Timeout: requestTimeout},
		xLogUID: uuidUpper(),
	}
}

// uuidNoDash 生成与 Node crypto.randomUUID().replace(/-/g, ”) 一致的小写 UUID。
func uuidNoDash() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return hex.EncodeToString(raw)
}

// uuidUpper 生成与上游 buildUuid（crypto.randomUUID().toUpperCase()）一致的大写 UUID。
func uuidUpper() string {
	raw := uuidNoDash()
	if len(raw) != 32 {
		return strings.ToUpper(raw)
	}
	return strings.ToUpper(raw[0:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:])
}

func randomBytes(n int) []byte {
	raw := make([]byte, n)
	_, _ = rand.Read(raw)
	return raw
}

func (c *campClient) traceparent() string {
	return "00-" + hex.EncodeToString(randomBytes(16)) + "-" + hex.EncodeToString(randomBytes(8)) + "-01"
}

func (c *campClient) timestamp(a *campAuth) int64 {
	return time.Now().UnixMilli() + a.ServerTimeOffsetMs
}

func (c *campClient) nonce(prefix string, timestamp int64) string {
	return prefix + uuidNoDash() + ":" + strconv.FormatInt(timestamp, 10)
}

// userKey 从登录响应里的 userKey 或 encodeRes 解出。
func (c *campClient) userKey(a *campAuth) (string, error) {
	if a.UserKey != "" {
		return a.UserKey, nil
	}
	if a.EncodeRes == "" {
		return "", nil
	}
	plain, err := rsaPublicDecrypt(a.PublicKey, a.EncodeRes)
	if err != nil {
		return "", fmt.Errorf("encodeRes 无法解密，请重新登录该账号: %w", err)
	}
	var decoded struct {
		UserKey string `json:"userKey"`
	}
	if json.Unmarshal(plain, &decoded) != nil {
		return "", errors.New("encodeRes 解出的内容不是 JSON，请重新登录该账号")
	}
	return decoded.UserKey, nil
}

// encodeParam 用 userKey 做 XXTEA 加密；userKey 缺失时返回空串，由调用方改走 specialEncodeParam。
func (c *campClient) encodeParam(a *campAuth) (string, error) {
	key, err := c.userKey(a)
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", nil
	}
	timestamp := c.timestamp(a)
	payload := fmt.Sprintf(`{"timestamp":%d,"nonce":"%s"}`, timestamp, c.nonce(a.UserID+":", timestamp))
	return base64.StdEncoding.EncodeToString(xxteaEncrypt([]byte(payload), []byte(key))), nil
}

func (c *campClient) specialEncodeParam(a *campAuth) (string, error) {
	timestamp := c.timestamp(a)
	payload := fmt.Sprintf(`{"timestamp":%d,"nonce":"%s"}`, timestamp, c.nonce(":", timestamp))
	out, err := rsaPublicEncrypt(a.PublicKey, []byte(payload))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

func (c *campClient) authHeaders(a *campAuth, fullURL string) (map[string]string, error) {
	a.applyDefaults()
	host := campGameHost
	if strings.Contains(fullURL, campMainURL) {
		host = campMainHost
	}
	headers := map[string]string{
		"Host":               host,
		"Content-Type":       "application/json; charset=UTF-8",
		"User-Agent":         a.UserAgent,
		"Content-Encrypt":    a.ContentEncrypt,
		"Accept-Encrypt":     a.AcceptEncrypt,
		"NOENCRYPT":          a.NoEncrypt,
		"X-Client-Proto":     a.XClientProto,
		"x-log-uid":          c.logUID(a),
		"traceparent":        c.trace(a),
		"istrpcrequest":      a.IsTrpcRequest,
		"cchannelid":         a.CChannelID,
		"cclientversioncode": a.CClientVerCode,
		"cclientversionname": a.CClientVerName,
		"ccurrentgameid":     a.CCurrentGameID,
		"cgameid":            a.CGameID,
		"cgzip":              a.CGzip,
		"cisarm64":           a.CIsArm64,
		"crand":              strconv.FormatInt(time.Now().UnixMilli(), 10),
		"csupportarm64":      a.CSupportArm64,
		"csystem":            a.CSystem,
		"csystemversioncode": a.CSysVerCode,
		"csystemversionname": a.CSysVerName,
		"cpuhardware":        a.CPUHardware,
		"gameareaid":         a.GameAreaID,
		"gameid":             a.CGameID,
		"gameusersex":        a.GameUserSex,
		"tinkerid":           a.TinkerID,
		"token":              a.Token,
		"userid":             a.UserID,
		"kohdimgender":       a.KohDimGender,
	}
	if a.OpenID != "" {
		headers["openid"] = a.OpenID
	}
	if a.GameOpenID != "" {
		headers["gameopenid"] = a.GameOpenID
	}
	if a.GameRoleID != "" {
		headers["gameroleid"] = a.GameRoleID
	}
	if a.GameServerID != "" {
		headers["gameserverid"] = a.GameServerID
	}
	param, err := c.encodeParam(a)
	if err != nil {
		return nil, err
	}
	if param != "" {
		headers["encodeParam"] = param
	} else {
		special, err := c.specialEncodeParam(a)
		if err != nil {
			return nil, fmt.Errorf("生成 specialEncodeParam 失败: %w", err)
		}
		headers["specialEncodeParam"] = special
	}
	return headers, nil
}

func (c *campClient) logUID(a *campAuth) string {
	if a.XLogUID != "" {
		return a.XLogUID
	}
	return c.xLogUID
}

func (c *campClient) trace(a *campAuth) string {
	if a.Traceparent != "" {
		return a.Traceparent
	}
	return c.traceparent()
}

// call 发送一次主站 JSON 请求，返回 data 字段；业务码非 0 时转成 campError。
func (c *campClient) call(ctx context.Context, endpoint string, body any, a *campAuth) (json.RawMessage, error) {
	code, msg, data, _, err := c.callRaw(ctx, endpoint, body, a)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, businessError(code, msg)
	}
	return data, nil
}

// callRaw 与 call 相同，但把业务码原样返回而不抛错。
// 签到（-105203 已签）、兑换（-10530338 币不足）这类需要按码分支的接口靠它。
// full 是解密后的完整响应体（部分接口如 lotteryquery 把字段放在顶层、不套 data）。
func (c *campClient) callRaw(ctx context.Context, endpoint string, body any, a *campAuth) (code int, msg string, data, full json.RawMessage, err error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, "", nil, nil, err
	}
	url := campMainURL + endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return 0, "", nil, nil, err
	}
	headers, err := c.authHeaders(a, url)
	if err != nil {
		return 0, "", nil, nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	req.Host = headers["Host"]

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", nil, nil, fmt.Errorf("请求营地接口失败: %w", err)
	}
	defer resp.Body.Close()

	if msg := headerValue(resp.Header, "encryptparamerr"); msg != "" {
		return 0, "", nil, nil, fmt.Errorf("接口安全参数校验失败 (encryptParamErr=%s)，请更新当前账号的 token / userKey / encodeRes 或客户端参数", msg)
	}
	codeHeader := headerValue(resp.Header, "returncode")
	msgHeader := decodeHeaderValue(headerValue(resp.Header, "returnmsg"))

	text, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return 0, "", nil, nil, fmt.Errorf("读取营地响应失败: %w", err)
	}

	payload := string(text)
	if headerValue(resp.Header, "campencrypt") == "true" {
		key, err := c.userKey(a)
		if err != nil {
			return 0, "", nil, nil, err
		}
		if key == "" {
			return 0, "", nil, nil, errors.New("接口响应已加密，但当前登录态缺少 userKey 或 encodeRes")
		}
		cipherText, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
		if err != nil {
			return 0, "", nil, nil, fmt.Errorf("营地响应解密失败，请检查当前账号的 userKey / encodeRes 或重新扫码登录: %w", err)
		}
		plain := xxteaDecrypt(cipherText, []byte(key))
		if len(plain) == 0 && len(cipherText) > 0 {
			return 0, "", nil, nil, errors.New("营地响应解密失败，请检查当前账号的 userKey / encodeRes 或重新扫码登录")
		}
		payload = strings.TrimRight(string(plain), "\x00")
	}

	trimmed := strings.TrimSpace(payload)
	var envelope struct {
		ReturnCode scalarValue     `json:"returnCode"`
		ReturnMsg  string          `json:"returnMsg"`
		Message    string          `json:"message"`
		Msg        string          `json:"msg"`
		Data       json.RawMessage `json:"data"`
	}
	parsed := false
	if trimmed != "" {
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			return 0, "", nil, nil, errors.New("接口返回无法解析，请检查当前使用账号的安全参数是否完整")
		}
		parsed = true
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := ""
		if parsed {
			detail = firstNonEmpty(envelope.Message, envelope.ReturnMsg, envelope.Msg)
		} else {
			detail = trimmed
		}
		if detail == "" {
			detail = http.StatusText(resp.StatusCode)
		}
		return 0, "", nil, nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, detail)
	}

	// 空响应 + returnCode 响应头是营地业务错误的常见形态（频控 -30107、隐藏主页 -10107）。
	if !parsed {
		if codeHeader == "" {
			return 0, "", nil, nil, errors.New("接口返回无法解析，请检查当前使用账号的安全参数是否完整")
		}
		code, err = strconv.Atoi(codeHeader)
		if err != nil {
			return 0, "", nil, nil, errors.New("接口返回无法解析，请检查当前使用账号的安全参数是否完整")
		}
		return code, msgHeader, nil, nil, nil
	}

	code64, _ := envelope.ReturnCode.Int64()
	code = int(code64)
	msg = firstNonEmpty(envelope.ReturnMsg, envelope.Message, envelope.Msg)
	return code, msg, envelope.Data, json.RawMessage(trimmed), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func businessError(code int, msg string) error {
	switch code {
	case -30107:
		return ErrRateLimited
	case -30003:
		return ErrAuthInvalid
	case -10107:
		return ErrProfileHidden
	case -30408, -10110:
		return ErrSeasonHidden
	}
	if msg == "" {
		msg = fmt.Sprintf("营地接口返回错误码 %d", code)
	}
	return &campError{code: code, msg: msg}
}

func headerValue(header http.Header, name string) string {
	if v := header.Get(name); v != "" {
		return v
	}
	for key, values := range header {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

// decodeHeaderValue 处理营地返回的中文 header（form-urlencoded，空格是 + 而不是 %20）。
func decodeHeaderValue(value string) string {
	if value == "" {
		return ""
	}
	if decoded, err := url.QueryUnescape(value); err == nil {
		return decoded
	}
	return value
}

// scalarValue 同时接受 JSON 字符串与数字，保留原始文本，
// 避免营地某些字段（gameSeq、gradeGame 等）在不同接口里类型不一致导致 Unmarshal 失败。
type scalarValue string

func (v *scalarValue) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "" || text == "null" {
		*v = ""
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		*v = scalarValue(str)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*v = scalarValue(number.String())
	return nil
}

func (v scalarValue) String() string { return string(v) }

func (v scalarValue) Int64() (int64, error) {
	text := strings.TrimSpace(string(v))
	if text == "" {
		return 0, nil
	}
	if value, err := strconv.ParseInt(text, 10, 64); err == nil {
		return value, nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, err
	}
	return int64(value), nil
}

func (v scalarValue) Float64() (float64, error) {
	text := strings.TrimSpace(string(v))
	if text == "" {
		return 0, nil
	}
	return strconv.ParseFloat(text, 64)
}

// Int 忽略错误的便捷转换，空串按 0。
func (v scalarValue) Int() int { n, _ := v.Int64(); return int(n) }

// Float 忽略错误的便捷转换，空串按 0。
func (v scalarValue) Float() float64 { f, _ := v.Float64(); return f }

type battleListData struct {
	List        []battleItem `json:"list"`
	HasMore     bool         `json:"hasMore"`
	LastTime    scalarValue  `json:"lastTime"`
	InvisDes    string       `json:"invisDes"`
	Invisible   bool         `json:"invisible"`
	RoleJobName string       `json:"roleJobName"`
	// Gaming 只在有人在对局时出现，是「当前这一局」的快照（见 watch.go）。
	Gaming *gamingInfo `json:"gaming"`
}

type battleItem struct {
	GameSeq      scalarValue `json:"gameSeq"`
	BattleType   scalarValue `json:"battleType"`
	GameSvr      scalarValue `json:"gameSvrId"`
	RelaySvr     scalarValue `json:"relaySvrId"`
	TargetRoleID scalarValue `json:"targetRoleId"`
	// BattleDetailURL 里带 toAppRoleId，详情接口的 targetRoleId 取不到时用它兜底。
	BattleDetailURL string `json:"battleDetailUrl"`

	HeroIcon      string      `json:"heroIcon"`
	MapName       string      `json:"mapName"`
	GameTime      string      `json:"gametime"`
	UsedTime      int         `json:"usedTime"`
	KillCnt       int         `json:"killcnt"`
	DeadCnt       int         `json:"deadcnt"`
	AssistCnt     int         `json:"assistcnt"`
	GameResult    int         `json:"gameresult"`
	Desc          string      `json:"desc"`
	GradeGame     scalarValue `json:"gradeGame"`
	EvaluateURLV3 string      `json:"evaluateUrlV3"`
	EvaluateURLV2 string      `json:"evaluateUrlV2"`
	EvaluateURL   string      `json:"evaluateUrl"`
	MvpURLV3      string      `json:"mvpUrlV3"`
	MvpURLV2      string      `json:"mvpUrlV2"`

	GodLikeCnt    int    `json:"godLikeCnt"`
	RampageCnt    int    `json:"hero1RampageCnt"`
	UltraKillCnt  int    `json:"hero1UltraKillCnt"`
	TripleKillCnt int    `json:"hero1TripleKillCnt"`
	RoleJobName   string `json:"roleJobName"`

	// P2 归档与赛季/趋势统计需要的字段。
	HeroID              scalarValue `json:"heroId"`
	MvpCnt              scalarValue `json:"mvpcnt"`
	LoseMvp             scalarValue `json:"losemvp"`
	RoleJob             scalarValue `json:"roleJob"`
	Stars               scalarValue `json:"stars"`
	OldMasterMatchScore scalarValue `json:"oldMasterMatchScore"`
	NewMasterMatchScore scalarValue `json:"newMasterMatchScore"`
	DtEventTime         scalarValue `json:"dtEventTime"`
}

// eventTime 统一取对局时间戳（秒）；营地不同版本会给 dtEventTime 或 gametime。
func (item battleItem) eventTime() int64 {
	if value := item.DtEventTime.Int(); value > 0 {
		return int64(value)
	}
	if item.GameTime == "" {
		return 0
	}
	value, err := strconv.ParseInt(item.GameTime, 10, 64)
	if err != nil {
		return 0
	}
	if value > 1e12 {
		return value / 1000
	}
	return value
}

// getMoreBattleList 拉取最近战绩。option: 0 全部 / 1 排位 / 4 巅峰。
func (c *campClient) getMoreBattleList(ctx context.Context, campID string, a *campAuth, option int) (*battleListData, error) {
	return c.getMoreBattleListPage(ctx, campID, a, option, "0")
}

// getMoreBattleListPage 按 lastTime 游标翻页；第一页传 "0"，之后传上一页响应的 LastTime。
func (c *campClient) getMoreBattleListPage(ctx context.Context, campID string, a *campAuth, option int, lastTime string) (*battleListData, error) {
	if lastTime == "" {
		lastTime = "0"
	}
	data, err := c.call(ctx, "/game/morebattlelist", map[string]any{
		"lastTime":         lastTime,
		"recommendPrivacy": 0,
		"apiVersion":       5,
		"friendUserId":     campID,
		"option":           option,
	}, a)
	if err != nil {
		return nil, err
	}
	var out battleListData
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------- 单场战绩详情 ----------

// looseBool 兼容营地既可能给 true/false，也可能给 1/0 的布尔位。
type looseBool bool

func (b *looseBool) UnmarshalJSON(data []byte) error {
	switch strings.TrimSpace(string(data)) {
	case "true", "1", `"1"`, `"true"`:
		*b = true
	default:
		*b = false
	}
	return nil
}

type battleDetailData struct {
	Head      battleDetailHead   `json:"head"`
	Battle    battleDetailBattle `json:"battle"`
	RedTeam   battleDetailTeam   `json:"redTeam"`
	BlueTeam  battleDetailTeam   `json:"blueTeam"`
	RedRoles  []battleDetailRole `json:"redRoles"`
	BlueRoles []battleDetailRole `json:"blueRoles"`
}

type battleDetailHead struct {
	AcntCamp   scalarValue `json:"acntCamp"`
	GameResult scalarValue `json:"gameResult"`
	MapName    string      `json:"mapName"`
	MatchDesc  string      `json:"matchDesc"`
	Tips       string      `json:"tips"`
	RoleID     scalarValue `json:"roleId"`
	HeroName   string      `json:"heroName"`
}

type battleDetailBattle struct {
	StartTime string      `json:"startTime"`
	UsedTime  scalarValue `json:"usedTime"`
}

type battleDetailTeam struct {
	AcntCamp  scalarValue `json:"acntCamp"`
	Money     scalarValue `json:"money"`
	TowerCnt  scalarValue `json:"towerCnt"`
	KillCnt   scalarValue `json:"killCnt"`
	DeadCnt   scalarValue `json:"deadCnt"`
	AssistCnt scalarValue `json:"assistCnt"`

	Bdragon1 scalarValue `json:"bdragon1"`
	Bdragon2 scalarValue `json:"bdragon2"`
	Bdragon3 scalarValue `json:"bdragon3"`
	Ldragon1 scalarValue `json:"ldragon1"`
	Ldragon2 scalarValue `json:"ldragon2"`
}

type battleDetailHero struct {
	HeroIcon string `json:"heroIcon"`
	HeroName string `json:"heroName"`
}

type battleDetailRole struct {
	BasicInfo struct {
		RoleName string      `json:"roleName"`
		RoleID   scalarValue `json:"roleId"`
		IsMe     looseBool   `json:"isMe"`
	} `json:"basicInfo"`
	BattleStats struct {
		KillCnt          scalarValue `json:"killCnt"`
		DeadCnt          scalarValue `json:"deadCnt"`
		AssistCnt        scalarValue `json:"assistCnt"`
		Money            scalarValue `json:"money"`
		GradeGame        scalarValue `json:"gradeGame"`
		TotalHeroHurtCnt scalarValue `json:"totalHeroHurtCnt"`
		TotalBeHurtCnt   scalarValue `json:"totalBeheroHurtCnt"`
		JoinGamePercent  scalarValue `json:"joinGamePercent"`
		CtrlTime         scalarValue `json:"ctrlTime"`
		KillSoldier      scalarValue `json:"killSoldier"`
		Mvp              looseBool   `json:"mvp"`
		EvaluateIconV3   string      `json:"evaluateIconV3"`
		EvaluateIconV2   string      `json:"evaluateIconV2"`
		EvaluateIcon     string      `json:"evaluateIcon"`
		MaxMoney         looseBool   `json:"maxMoney"`
		MaxJoinGame      looseBool   `json:"maxJoinGamePercent"`
		MaxBeHurt        looseBool   `json:"maxBeheroHurt"`
	} `json:"battleStats"`
	BattleRecords struct {
		UsedHero struct {
			HeroIcon string `json:"heroIcon"`
			HeroName string `json:"heroName"`
		} `json:"usedHero"`
		UsedSkin struct {
			SkinIcon string `json:"skinIcon128128"`
			SkinName string `json:"skinName"`
		} `json:"usedSkin"`
		Skill struct {
			SkillIcon string `json:"skillIcon"`
		} `json:"skill"`
		FinalEquips []struct {
			EquipIcon string `json:"equipIcon"`
		} `json:"finalEquips"`
	} `json:"battleRecords"`
}

// getBattleDetail 拉单场详情。参数全在战绩列表项里，不用额外请求。
func (c *campClient) getBattleDetail(ctx context.Context, campID string, a *campAuth, item battleItem) (*battleDetailData, error) {
	targetRoleID := item.TargetRoleID.String()
	if targetRoleID == "" {
		if matched := roleIDFromURLRe.FindStringSubmatch(item.BattleDetailURL); matched != nil {
			targetRoleID = matched[1]
		}
	}
	if targetRoleID == "" {
		targetRoleID = "0"
	}
	data, err := c.call(ctx, "/game/battledetail", map[string]any{
		"recommendPrivacy": 0,
		"battleType":       item.BattleType.String(),
		"gameSvr":          item.GameSvr.String(),
		"relaySvr":         item.RelaySvr.String(),
		"targetRoleId":     targetRoleID,
		"gameSeq":          item.GameSeq.String(),
		"friendUserId":     campID,
	}, a)
	if err != nil {
		return nil, err
	}
	var out battleDetailData
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type profileRole struct {
	RoleID      scalarValue `json:"roleId"`
	RoleName    string      `json:"roleName"`
	RoleJobName string      `json:"roleJobName"`
	RoleIcon    string      `json:"roleIcon"`
	AreaName    string      `json:"areaName"`
	RoleText    string      `json:"roleText"`
	ServerID    scalarValue `json:"serverId"`

	// 在线状态三态：0 离线 / 1 在线 / 2 游戏客户端在线（大厅、匹配中都算 2）。
	// 三个字段全为 0 表示营地没给这个号的在线状态，不是离线。
	GameOnline  scalarValue `json:"gameOnline"`
	OnlineTime  scalarValue `json:"onlineTime"`
	OfflineTime scalarValue `json:"offlineTime"`
}

type profileData struct {
	RoleList     []profileRole `json:"roleList"`
	TargetRoleID scalarValue   `json:"targetRoleId"`
	Head         profileHead   `json:"head"`
}

// profileHead 的 mods 按 modId 区分（701=5v5 段位、702=巅峰分、304=战斗力、
// 401=总场次、408=MVP、409=胜率、201=英雄、202=皮肤、601=最高战力英雄）。
type profileHead struct {
	Mods []profileMod `json:"mods"`
}

type profileMod struct {
	ModID   scalarValue `json:"modId"`
	Name    string      `json:"name"`
	Content string      `json:"content"`
	Param1  string      `json:"param1"`
}

// getProfile 拉取营地主页的角色列表，用于账号卡片上的角色名与头像。
func (c *campClient) getProfile(ctx context.Context, campID string, a *campAuth) (*profileData, error) {
	data, err := c.call(ctx, "/game/koh/profile", map[string]any{
		"targetUserId":     campID,
		"targetRoleId":     "0",
		"resVersion":       "3",
		"recommendPrivacy": "0",
		"apiVersion":       "2",
	}, a)
	if err != nil {
		return nil, err
	}
	var out profileData
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

/* ------------------------------------------------------------------ *
 * P2：赛季表现 / 巅峰数据
 * ------------------------------------------------------------------ */

// seasonBranch 分路统计。winRate 在 behavior 里是 "62%" 字符串、historyList 里没有，
// 一律按字符串保存，由展示层解析。
type seasonBranch struct {
	BranchType scalarValue `json:"branchType"`
	BranchName string      `json:"branchName"`
	WinNum     scalarValue `json:"winNum"`
	LoseNum    scalarValue `json:"loseNum"`
	WinRate    scalarValue `json:"winRate"`
	GameCnt    scalarValue `json:"gameCnt"`
}

type seasonHero struct {
	HeroID            scalarValue `json:"heroId"`
	HeroName          string      `json:"heroName"`
	HeroIcon          string      `json:"heroIcon"`
	HeroLandscapeIcon string      `json:"heroLandscapeIcon"`
	HonorTitle        *honorTitle `json:"honorTitle"`
	WinRate           scalarValue `json:"winRate"` // 0~1 小数
	GameCnt           scalarValue `json:"gameCnt"`
	WinCnt            scalarValue `json:"winCnt"`
	HeroFightPower    scalarValue `json:"heroFightPower"`
}

type gameTrendPoint struct {
	TotalRankStar scalarValue `json:"totalRankStar"`
	Stars         scalarValue `json:"stars"`
	JobName       string      `json:"jobName"`
	JobColor      string      `json:"jobColor"`
	Time          scalarValue `json:"time"`
	Score         scalarValue `json:"score"`
}

// seasonRankInfo 同时覆盖 behavior.rankInfo（当前赛季）与 historyList[].rankInfo（历史赛季）。
type seasonRankInfo struct {
	Branches  []seasonBranch   `json:"branches"`
	Heros     []seasonHero     `json:"heros"`
	GameTrend []gameTrendPoint `json:"gameTrend"`

	TotalCnt            scalarValue `json:"totalCnt"`
	TotalWinCnt         scalarValue `json:"totalWinCnt"`
	MaxContinuousWinCnt scalarValue `json:"maxContinuousWinCnt"`
	GoldCnt             scalarValue `json:"goldCnt"`
	SilverCnt           scalarValue `json:"silverCnt"`
	WinRate             scalarValue `json:"winRate"`
	AverageScore        scalarValue `json:"averageScore"`
	JobName             string      `json:"jobName"`
	ShortJobName        string      `json:"shortJobName"`
	RankingStar         scalarValue `json:"rankingStar"`
	TotalRankStar       scalarValue `json:"totalRankStar"`
}

// seasonMasterInfo 同时覆盖 behavior.masterInfo 与 historyList[].masterInfo。
type seasonMasterInfo struct {
	TotalGameCnt scalarValue    `json:"totalGameCnt"`
	Branches     []seasonBranch `json:"branches"`
	Heros        []seasonHero   `json:"heros"`

	MasterScore         scalarValue `json:"masterScore"`
	TotalCnt            scalarValue `json:"totalCnt"`
	TotalWinCnt         scalarValue `json:"totalWinCnt"`
	MaxContinuousWinCnt scalarValue `json:"maxContinuousWinCnt"`
	WinRate             scalarValue `json:"winRate"`
	AverageScore        scalarValue `json:"averageScore"`
	GoldCnt             scalarValue `json:"goldCnt"`
	SilverCnt           scalarValue `json:"silverCnt"`
}

// battleStats 五维与荣誉（万分制），历史赛季为 null。
type battleStats struct {
	HurtHero  scalarValue `json:"hurtHero"`
	Survive   scalarValue `json:"survive"`
	Battle    scalarValue `json:"battle"`
	Grow      scalarValue `json:"grow"`
	Kda       scalarValue `json:"kda"`
	Mvp       scalarValue `json:"mvp"`
	LoseMvp   scalarValue `json:"loseMvp"`
	ThreeKill scalarValue `json:"threeKill"`
	FourKill  scalarValue `json:"fourKill"`
	FiveKill  scalarValue `json:"fiveKill"`
	GodLike   scalarValue `json:"godLike"`
}

type seasonHistory struct {
	SeasonID   scalarValue      `json:"seasonId"`
	SeasonName string           `json:"seasonName"`
	StartTime  scalarValue      `json:"startTime"`
	EndTime    scalarValue      `json:"endTime"`
	RankInfo   seasonRankInfo   `json:"rankInfo"`
	MasterInfo seasonMasterInfo `json:"masterInfo"`
}

type seasonHeadCard struct {
	RoleName    string      `json:"roleName"`
	RoleIcon    string      `json:"roleIcon"`
	ServerName  string      `json:"serverName"`
	AreaName    string      `json:"areaName"`
	JobName     string      `json:"jobName"`
	RankingStar scalarValue `json:"rankingStar"`
	MasterScore scalarValue `json:"masterScore"`
	MasterRank  scalarValue `json:"masterRank"`
	Score       scalarValue `json:"score"`
	GameCnt     scalarValue `json:"gameCnt"`
	WinRate     scalarValue `json:"winRate"`
	Branch      string      `json:"branch"`
	WangzheCnt  scalarValue `json:"wangzheCnt"`
}

type seasonBehavior struct {
	RankInfo   seasonRankInfo   `json:"rankInfo"`
	MasterInfo seasonMasterInfo `json:"masterInfo"`
}

type seasonPageData struct {
	HeadCard    seasonHeadCard  `json:"headCard"`
	Behavior    seasonBehavior  `json:"behavior"`
	BattleStats *battleStats    `json:"battleStats"`
	HistoryList []seasonHistory `json:"historyList"`
}

// getSeasonpage 拉赛季表现。seasonID 不是赛季号：0=全赛季汇总（headCard 汇总 +
// 完整 historyList），具体赛季要用 seasonId=0 响应里 historyList 的 seasonId 再查一次。
func (c *campClient) getSeasonpage(ctx context.Context, roleID string, seasonID int, a *campAuth) (*seasonPageData, error) {
	data, err := c.call(ctx, "/game/seasonpage", map[string]any{
		"recommendPrivacy": 0,
		"seasonId":         seasonID,
		"roleId":           roleID,
	}, a)
	if err != nil {
		return nil, err
	}
	var out seasonPageData
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// fightDataSelf 是 getfightdata 的核心统计。五维 hurtHero/survive/battle/grow/kda 万分制。
type fightDataSelf struct {
	WinNum      scalarValue `json:"winNum"`
	LoseNum     scalarValue `json:"loseNum"`
	WinRate     scalarValue `json:"winRate"`
	AvgScore    scalarValue `json:"avgScore"`
	HurtHero    scalarValue `json:"hurtHero"`
	Survive     scalarValue `json:"survive"`
	Battle      scalarValue `json:"battle"`
	Grow        scalarValue `json:"grow"`
	Kda         scalarValue `json:"kda"`
	Mvp         scalarValue `json:"mvp"`
	LoseMvp     scalarValue `json:"loseMvp"`
	ThreeKill   scalarValue `json:"threeKill"`
	FourKill    scalarValue `json:"fourKill"`
	FiveKill    scalarValue `json:"fiveKill"`
	GodLike     scalarValue `json:"godLike"`
	GoldMedal   scalarValue `json:"goldMedal"`
	SliverMedal scalarValue `json:"sliverMedal"`
}

type fightData struct {
	BattleDataSelf fightDataSelf `json:"battleDataSelf"`
}

// getFightData 拉近 30 天/30 场的战斗数据。gameBattleType: 10=巅峰赛 2=5v5 3=排位赛；
// branchType: 0=全部 1=对抗路 2=中路 3=发育路 4=打野 5=游走；dateType: 2=近30天 1=近30场。
func (c *campClient) getFightData(ctx context.Context, roleID string, gameBattleType, branchType, dateType int, a *campAuth) (*fightDataSelf, error) {
	if dateType == 0 {
		dateType = 2
	}
	data, err := c.call(ctx, "/game/getfightdata", map[string]any{
		"recommendPrivacy": 0,
		"dateType":         dateType,
		"roleId":           roleID,
		"roleFriendId":     0,
		"branchType":       branchType,
		"source":           1,
		"gameBattleType":   gameBattleType,
		"card":             0,
	}, a)
	if err != nil {
		return nil, err
	}
	var out fightData
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out.BattleDataSelf, nil
}

/* ------------------------------------------------------------------ *
 * P2：游戏侧 form 接口（英雄/皮肤列表）
 * 与主站不同：form-urlencoded 请求体、裸 JSON 响应（可能带 returnCode）。
 * ------------------------------------------------------------------ */

const campGameFormURL = "https://ssl.kohsocialapp.qq.com:10001"

// gameFormCall 发送一次游戏侧 form 请求，返回整个响应 JSON（没有 data 包装）。
func (c *campClient) gameFormCall(ctx context.Context, endpoint string, extra map[string]string, a *campAuth) (json.RawMessage, error) {
	a.applyDefaults()
	fields := map[string]string{
		"cChannelId":         a.CChannelID,
		"cClientVersionCode": a.CClientVerCode,
		"cClientVersionName": a.CClientVerName,
		"cCurrentGameId":     a.CCurrentGameID,
		"cGameId":            a.CGameID,
		"cGzip":              a.CGzip,
		"cIsArm64":           a.CIsArm64,
		"cRand":              strconv.FormatInt(time.Now().UnixMilli(), 10),
		"cSupportArm64":      a.CSupportArm64,
		"cSystem":            a.CSystem,
		"cSystemVersionCode": a.CSysVerCode,
		"cSystemVersionName": a.CSysVerName,
		"cpuHardware":        a.CPUHardware,
		"gameAreaId":         a.GameAreaID,
		"gameId":             a.CGameID,
		"gameRoleId":         firstNonEmpty(a.GameRoleID, "0"),
		"gameServerId":       firstNonEmpty(a.GameServerID, "0"),
		"gameUserSex":        a.GameUserSex,
		"openId":             firstNonEmpty(a.OpenID, c.logUID(a)),
		"tinkerId":           a.TinkerID,
		"token":              a.Token,
		"userId":             a.UserID,
	}
	for key, value := range extra {
		fields[key] = value
	}
	form := url.Values{}
	for key, value := range fields {
		form.Set(key, value)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, campGameFormURL+endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", a.UserAgent)
	req.Header.Set("Content-Encrypt", "")
	req.Header.Set("Accept-Encrypt", "")
	req.Header.Set("NOENCRYPT", "1")
	req.Header.Set("X-Client-Proto", a.XClientProto)
	req.Header.Set("x-log-uid", c.logUID(a))
	req.Header.Set("kohdimgender", a.KohDimGender)
	req.Header.Set("token", a.Token)
	req.Header.Set("userid", a.UserID)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求营地接口失败: %w", err)
	}
	defer resp.Body.Close()

	text, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("读取营地响应失败: %w", err)
	}
	trimmed := strings.TrimSpace(string(text))
	if trimmed == "" {
		return nil, errors.New("接口返回无法解析，请检查当前账号登录态是否有效")
	}
	var envelope struct {
		ReturnCode scalarValue `json:"returnCode"`
		ReturnMsg  string      `json:"returnMsg"`
		Message    string      `json:"message"`
	}
	if err := json.Unmarshal(text, &envelope); err != nil {
		return nil, errors.New("接口返回无法解析，请检查当前账号登录态是否有效")
	}
	// 裸 JSON（无 returnCode）与 0 都算成功；非 0 按业务错误处理。
	if code, err := envelope.ReturnCode.Int64(); err == nil && code != 0 {
		return nil, businessError(int(code), firstNonEmpty(envelope.ReturnMsg, envelope.Message))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return json.RawMessage(text), nil
}

/* ------------------------------ 我的英雄 ------------------------------ */

// gameHero 是 /play/h5getherolist 的单条英雄（全部竞技模式生涯累计）。
type gameHero struct {
	HeroID         scalarValue `json:"heroId"`
	Name           string      `json:"name"`
	PlayNum        scalarValue `json:"playNum"`
	WinNum         scalarValue `json:"winNum"`
	WinRate        string      `json:"winRate"` // 字符串，自带 %（"53.8%"）
	HeroFightPower scalarValue `json:"heroFightPower"`
	SkilledLevel   scalarValue `json:"skilledLevel"`
	HeroTypes      []string    `json:"heroTypes"`
	URL            string      `json:"url"`
	HeroIcon       string      `json:"heroIcon"`
}

type gameHeroListData struct {
	HeroList []gameHero `json:"heroList"`
	HasData  struct {
		HeroNum scalarValue `json:"heroNum"`
	} `json:"hasData"`
}

// getGameHeroList 拉账号全量英雄列表（营地 App「我的英雄」页）。
func (c *campClient) getGameHeroList(ctx context.Context, campID string, a *campAuth) (*gameHeroListData, error) {
	raw, err := c.gameFormCall(ctx, "/play/h5getherolist", map[string]string{
		"noCache":          "0",
		"recommendPrivacy": "0",
		"friendUserId":     campID,
	}, a)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data gameHeroListData `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	data := &out.Data
	if data.HeroList == nil {
		// 部分响应数据在顶层而不是 data 下
		var flat gameHeroListData
		if err := json.Unmarshal(raw, &flat); err == nil && flat.HeroList != nil {
			data = &flat
		}
	}
	return data, nil
}

// honorTitle 荣耀称号（「XX区第N英雄」）。type: 1=县 2=市 3=省 4=国服。
type honorTitle struct {
	Type scalarValue `json:"type"`
	Desc struct {
		Full string `json:"full"`
		Name string `json:"name"`
		Abbr string `json:"abbr"`
	} `json:"desc"`
}

// usuallyHero 是 /hero/getseasonusaullyherolist 的单条（历史赛季口径）。
type usuallyHero struct {
	HeroID            scalarValue `json:"heroId"`
	HeroFightPower    scalarValue `json:"heroFightPower"`
	MaxHeroFightPower scalarValue `json:"maxHeroFightPower"`
	HonorTitle        *honorTitle `json:"honorTitle"`
}

// getSeasonUsuallyHeroList 拉「我的英雄 · 历史赛季」页（seasonId=0 跨赛季峰值）。
// 注意路径里的 usaully 是营地自己的拼写，不要改成 usually。
func (c *campClient) getSeasonUsuallyHeroList(ctx context.Context, roleID string, seasonID int, a *campAuth) ([]usuallyHero, error) {
	data, err := c.call(ctx, "/hero/getseasonusaullyherolist", map[string]any{
		"recommendPrivacy": 0,
		"seasonId":         seasonID,
		"roleId":           roleID,
	}, a)
	if err != nil {
		return nil, err
	}
	var out struct {
		List []usuallyHero `json:"list"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out.List, nil
}

// profileHero 是 /game/profile/herolist 的单条（生涯累计榜，常用英雄兜底用）。
type profileHero struct {
	BasicInfo struct {
		HeroID         scalarValue `json:"heroId"`
		Title          string      `json:"title"`
		HeroFightPower scalarValue `json:"heroFightPower"`
		PlayNum        scalarValue `json:"playNum"`
		WinRate        string      `json:"winRate"`
	} `json:"basicInfo"`
	HonorTitle *honorTitle `json:"honorTitle"`
}

// getProfileHeroList 拉生涯累计英雄榜（赛季无数据时的兜底）。
func (c *campClient) getProfileHeroList(ctx context.Context, campID, roleID string, a *campAuth) ([]profileHero, error) {
	data, err := c.call(ctx, "/game/profile/herolist", map[string]any{
		"targetUserId":     campID,
		"targetRoleId":     roleID,
		"recommendPrivacy": 0,
	}, a)
	if err != nil {
		return nil, err
	}
	var out struct {
		HeroList []profileHero `json:"heroList"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out.HeroList, nil
}

/* ------------------------------ 皮肤 ------------------------------ */

// skinOwnedItem 是 heroSkinList 的单条。已拥有判据：有 iBuy 字段且 szClass 非空，
// 其余条目只是营地顺带下发的展示项。
type skinOwnedItem struct {
	SkinID  scalarValue `json:"skinId"`
	IBuy    scalarValue `json:"iBuy"`
	SzClass string      `json:"szClass"`
}

// skinConf 是 heroSkinConfList 的单条（与账号无关的全量皮肤配置表）。
type skinConf struct {
	ISkinID       scalarValue `json:"iSkinId"`
	SzTitle       string      `json:"szTitle"`
	SzHeroTitle   string      `json:"szHeroTitle"`
	SzClass       string      `json:"szClass"`
	ClassTypeName []string    `json:"classTypeName"`
	SkinWorth     scalarValue `json:"skin_worth"`
	IPrice        scalarValue `json:"iPrice"`
	SzLargeIcon   string      `json:"szLargeIcon"`
	BigCover      string      `json:"bigCover"`
	SzSmallIcon   string      `json:"szSmallIcon"`
	ClassLabel    string      `json:"classLabel"`
	IsHidden      scalarValue `json:"isHidden"`
}

type skinCountInfo struct {
	Owned        scalarValue `json:"owned"`
	TotalSkinNum scalarValue `json:"totalSkinNum"`
	NotForSell   scalarValue `json:"notForSell"`
	TotalValue   scalarValue `json:"totalValue"`
}

type skinListData struct {
	SkinCountInfo    skinCountInfo       `json:"skinCountInfo"`
	HeroSkinList     []skinOwnedItem     `json:"heroSkinList"`
	HeroSkinConfList map[string]skinConf `json:"heroSkinConfList"`
}

// getSkinList 拉账号皮肤列表（皮肤墙）。游戏侧 form 接口，响应不加密。
func (c *campClient) getSkinList(ctx context.Context, campID string, a *campAuth) (*skinListData, error) {
	raw, err := c.gameFormCall(ctx, "/play/h5getheroskinlist", map[string]string{
		"noCache":          "0",
		"recommendPrivacy": "0",
		"friendUserId":     campID,
	}, a)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data skinListData `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	data := &out.Data
	if data.HeroSkinList == nil && data.HeroSkinConfList == nil {
		var flat skinListData
		if err := json.Unmarshal(raw, &flat); err == nil && (flat.HeroSkinList != nil || flat.HeroSkinConfList != nil) {
			data = &flat
		}
	}
	return data, nil
}
