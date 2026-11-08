package gloryofkings

// 营地扫码登录：微信走纯 HTTP，QQ 走同一个浏览器会话。
// 常量、URL、表单字段顺序、签名算法与状态码均照抄上游
// sources/standalone/glory-of-kings/utils/wechatLogin.js 与 qqLogin.js。

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"shizukubot/sdk"
)

const (
	// 微信开放平台 AppID —— 营地 App 内置的那个。
	wxAppID = "wxf4b1e8a3e9aaf978"

	// 营地网关：取 SDK ticket、登录换号都走它。
	campAPIBase = "https://ssl.kohsocialapp.qq.com:10001"

	// 微信开放平台扫码 SDK：取二维码与轮询结果。
	wxQRCodeAPI     = "https://open.weixin.qq.com/connect/sdk/qrconnect"
	wxQRCodePollAPI = "https://long.open.weixin.qq.com/connect/l/qrconnect"

	// QQ 登录页：response_type=code 才能从 redirect query 里取到 code。
	qqAppID     = "1105200115"
	qqLoginPage = "https://openmobile.qq.com/oauth2.0/m_authorize?client_id=" + qqAppID +
		"&scope=all&redirect_uri=auth://tauth.qq.com/&style=qr&response_type=code"

	// 扫码页在手机 UA 下才给二维码版式。
	qqQRUserAgent = "Mozilla/5.0 (Linux; Android 15; V2366GA Build/V417IR; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/110.0.5481.154 Safari/537.36 tencent_game_emulator"

	// 设备指纹里的占位 MAC / 内存（营地只看格式）。
	deviceMACPlaceholder = "02:00:00:00:00:00"
	deviceMemBytes       = 12 * 1024 * 1024 * 1024

	loginQRCodeTTL      = 3 * time.Minute
	loginPollInterval   = 2 * time.Second
	loginRequestTimeout = 15 * time.Second

	qqPageTimeout   = 90 * time.Second
	qqQRWaitTimeout = 60 * time.Second
	qqScanTimeout   = 3 * time.Minute
)

var (
	// 营地接口公共头：声明不走加密、客户端是 https 协议。
	campCommonHeaders = map[string]string{
		"Content-Encrypt": "",
		"Accept-Encrypt":  "",
		"NOENCRYPT":       "1",
		"X-Client-Proto":  "https",
		"User-Agent":      "okhttp/4.9.1",
	}

	// 轮询状态码 → 业务错误。
	wxPollFailures = map[int]string{
		402: "登录二维码已过期，请重新发起",
		403: "登录已取消，请重新发起",
		500: "登录服务异常，请稍后再试",
	}

	loginHTTPClient = &http.Client{Timeout: loginRequestTimeout}

	qqCodePattern = regexp.MustCompile(`[?&]code=([0-9A-Za-z]+)`)

	qqQRSelectors = []string{
		"#qrlogin_img",
		".qrlogin_img",
		`img[src*="ptqrshow"]`,
		`img[src*="qrcode"]`,
		"canvas#qrlogin_canvas",
		".qrlogin canvas",
	}
)

/* ------------------------------------------------------------------ *
 * 基础工具
 * ------------------------------------------------------------------ */

func loginSleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func randomDigits(length int) string {
	var builder strings.Builder
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			builder.WriteByte('0')
			continue
		}
		builder.WriteByte(byte('0' + n.Int64()))
	}
	return builder.String()
}

func sha1Hex(input string) string {
	sum := sha1.Sum([]byte(input))
	return hex.EncodeToString(sum[:])
}

// encodeJPEG 把二维码统一转成 JPEG；解码失败时原样返回。
func encodeJPEG(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return raw
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		return raw
	}
	return buf.Bytes()
}

// rawText 把 JSON 标量转成字符串：字符串去引号，数字保留原始文本（避免精度丢失）。
func rawText(raw json.RawMessage) string {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return ""
	}
	if strings.HasPrefix(text, "\"") {
		var value string
		if json.Unmarshal(raw, &value) == nil {
			return value
		}
		return ""
	}
	return text
}

// rawIsZero 对应上游的 `returnCode !== 0` / `code !== 0` 严格比较。
func rawIsZero(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "0"
}

func httpStatusOK(status int) bool {
	return status >= 200 && status <= 299
}

// loginDoRequest 是全链路唯一的发请求入口：每次都带 15 秒超时。
func loginDoRequest(ctx context.Context, method, rawURL string, headers map[string]string, body string) (int, string, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return 0, "", fmt.Errorf("构造请求失败: %w", err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := loginHTTPClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, "", ctxErr
		}
		var netErr net.Error
		if (errors.As(err, &netErr) && netErr.Timeout()) || errors.Is(err, context.DeadlineExceeded) {
			// 与上游 fetchJson 的超时归一化文案一致。
			return 0, "", errors.New("营地接口无响应，请稍后重试（TimeoutError, code=23）")
		}
		return 0, "", fmt.Errorf("请求 %s 失败: %w", rawURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, "", fmt.Errorf("读取 %s 响应失败: %w", rawURL, err)
	}
	return resp.StatusCode, string(raw), nil
}

type formField struct {
	key   string
	value string
}

// formBody 按 insertion order 拼表单，不排序（营地按顺序校验）。
func formBody(fields []formField) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, url.QueryEscape(field.key)+"="+url.QueryEscape(field.value))
	}
	return strings.Join(parts, "&")
}

/* ------------------------------------------------------------------ *
 * 设备指纹
 * ------------------------------------------------------------------ */

// devicePayload 的字段顺序就是 JSON 顺序，营地按它校验，别重排。
type devicePayload struct {
	Timestamp           int64  `json:"timestamp"`
	Nonce               string `json:"nonce"`
	CDeviceID           string `json:"cDeviceId"`
	DeviceID            string `json:"deviceid"`
	CDeviceImei         string `json:"cDeviceImei"`
	CDeviceMac          string `json:"cDeviceMac"`
	CDevicePPI          int    `json:"cDevicePPI"`
	CDeviceScreenWidth  int    `json:"cDeviceScreenWidth"`
	CDeviceScreenHeight int    `json:"cDeviceScreenHeight"`
	CDeviceBrand        string `json:"cDeviceBrand"`
	CDeviceModel        string `json:"cDeviceModel"`
	CDeviceMem          int64  `json:"cDeviceMem"`
	CDeviceCPU          string `json:"cDeviceCPU"`
	CSystemVersionCode  string `json:"cSystemVersionCode"`
	CDeviceNet          string `json:"cDeviceNet"`
	CDeviceSP           string `json:"cDeviceSP"`
	CDeviceOaid         string `json:"cDeviceOaid"`
	DeviceLevel         int    `json:"deviceLevel"`
	Px                  int    `json:"px"`
	Py                  int    `json:"py"`
	WifiSsid            string `json:"wifi_ssid"`
	WifiMac             string `json:"wifi_mac"`
}

// buildSpecialEncodeParam：设备指纹 JSON → 分块 RSA → base64。
func buildSpecialEncodeParam() (string, error) {
	timestamp := time.Now().UnixMilli()
	// nonce 在前、deviceId 在后，顺序不能换。
	nonce := ":" + uuidNoDash() + ":" + strconv.FormatInt(timestamp, 10)
	deviceID := uuidNoDash()

	imei := deviceID
	if len(imei) > 15 {
		imei = imei[:15]
	}
	payload := devicePayload{
		Timestamp:           timestamp,
		Nonce:               nonce,
		CDeviceID:           deviceID,
		DeviceID:            deviceID,
		CDeviceImei:         imei,
		CDeviceMac:          deviceMACPlaceholder,
		CDevicePPI:          480,
		CDeviceScreenWidth:  1080,
		CDeviceScreenHeight: 2400,
		CDeviceBrand:        "OnePlus",
		CDeviceModel:        "PHK110",
		CDeviceMem:          deviceMemBytes,
		CDeviceCPU:          "SM8650",
		CSystemVersionCode:  "34",
		CDeviceNet:          "WIFI",
		CDeviceSP:           "China Mobile",
		CDeviceOaid:         deviceID,
		DeviceLevel:         3,
		Px:                  0,
		Py:                  0,
		WifiSsid:            "unknown",
		WifiMac:             deviceMACPlaceholder,
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("生成设备指纹失败: %w", err)
	}
	encrypted, err := rsaPublicEncrypt(defaultPublicKey, plain)
	if err != nil {
		return "", fmt.Errorf("加密设备指纹失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// buildDeviceParams 的字段顺序别动，表单 body 与请求头都照它排。
func buildDeviceParams() []formField {
	return []formField{
		{"cChannelId", "10003391"},
		{"cClientVersionCode", "2057957801"},
		{"cClientVersionName", "10.111.0323"},
		{"cCurrentGameId", "20001"},
		{"cGameId", "20001"},
		{"cGzip", "1"},
		{"cIsArm64", "true"},
		{"cRand", strconv.FormatInt(time.Now().UnixMilli(), 10)},
		{"cSupportArm64", "true"},
		{"cSystem", "android"},
		{"cSystemVersionCode", "34"},
		{"cSystemVersionName", "14"},
		{"cpuHardware", "qcom"},
		{"gameId", "20001"},
		{"tinkerId", "2057957801_64_0"},
	}
}

/* ------------------------------------------------------------------ *
 * 营地响应
 * ------------------------------------------------------------------ */

type campLoginData struct {
	UserID    json.RawMessage `json:"userId"`
	Token     json.RawMessage `json:"token"`
	EncodeRes json.RawMessage `json:"encodeRes"`
	AppOpenID json.RawMessage `json:"appOpenid"`
	Nickname  json.RawMessage `json:"nickname"`
	Avatar    json.RawMessage `json:"avatar"`
}

type campLoginResponse struct {
	ReturnCode json.RawMessage `json:"returnCode"`
	ReturnMsg  string          `json:"returnMsg"`
	Data       campLoginData   `json:"data"`
}

// decodeEncodeResUserKey 只要 encodeRes 里的 userKey，拿不到就返回空串。
func decodeEncodeResUserKey(encodeRes string) string {
	if encodeRes == "" {
		return ""
	}
	plain, err := rsaPublicDecrypt(defaultPublicKey, encodeRes)
	if err != nil {
		return ""
	}
	var payload struct {
		UserKey string `json:"userKey"`
	}
	if json.Unmarshal(plain, &payload) != nil {
		return ""
	}
	return payload.UserKey
}

func buildCredential(data campLoginData, platform string, openIDFallback string) *credential {
	userID := rawText(data.UserID)
	token := rawText(data.Token)
	encodeRes := rawText(data.EncodeRes)
	openID := rawText(data.AppOpenID)
	if openID == "" {
		openID = openIDFallback
	}
	return &credential{
		UserID:    userID,
		Token:     token,
		UserKey:   decodeEncodeResUserKey(encodeRes),
		EncodeRes: encodeRes,
		OpenID:    openID,
		Platform:  platform,
		Nickname:  rawText(data.Nickname),
		Avatar:    rawText(data.Avatar),
		SavedAt:   time.Now().Unix(),
	}
}

/* ------------------------------------------------------------------ *
 * 微信扫码（纯 HTTP）
 * ------------------------------------------------------------------ */

type wechatSession struct {
	xLogUID   string
	sdkTicket string
	uuid      string
}

type wechatQRCode struct {
	uuid  string
	image []byte
}

func fetchWxSdkTicket(ctx context.Context, xLogUID string) (string, error) {
	headers := map[string]string{}
	for key, value := range campCommonHeaders {
		headers[key] = value
	}
	headers["x-log-uid"] = xLogUID

	status, text, err := loginDoRequest(ctx, http.MethodPost, campAPIBase+"/a/getwxsdkticket", headers, "")
	if err != nil {
		return "", err
	}
	var envelope struct {
		ReturnCode json.RawMessage `json:"returnCode"`
		Data       struct {
			SdkTicket string `json:"sdkTicket"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(text), &envelope)

	if !httpStatusOK(status) || !rawIsZero(envelope.ReturnCode) || envelope.Data.SdkTicket == "" {
		return "", fmt.Errorf("获取登录 SDK Ticket 失败（HTTP %d），稍后再试", status)
	}
	return envelope.Data.SdkTicket, nil
}

func fetchWechatQrCode(ctx context.Context, ticket string) (*wechatQRCode, error) {
	nonce := randomDigits(8)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := sha1Hex("appid=" + wxAppID + "&noncestr=" + nonce + "&sdk_ticket=" + ticket + "&timestamp=" + timestamp)

	params := []formField{
		{"appid", wxAppID},
		{"noncestr", nonce},
		{"timestamp", timestamp},
		{"scope", "snsapi_userinfo"},
		{"signature", signature},
	}
	status, text, err := loginDoRequest(ctx, http.MethodGet, wxQRCodeAPI+"?"+formBody(params), nil, "")
	if err != nil {
		return nil, err
	}
	var envelope struct {
		ErrCode json.RawMessage `json:"errcode"`
		UUID    string          `json:"uuid"`
		QRCode  struct {
			QRCodeBase64 string `json:"qrcodebase64"`
		} `json:"qrcode"`
	}
	_ = json.Unmarshal([]byte(text), &envelope)

	if !httpStatusOK(status) || !rawIsZero(envelope.ErrCode) || envelope.QRCode.QRCodeBase64 == "" || envelope.UUID == "" {
		return nil, fmt.Errorf("获取登录二维码失败（HTTP %d），稍后再试", status)
	}
	image, err := base64.StdEncoding.DecodeString(envelope.QRCode.QRCodeBase64)
	if err != nil {
		return nil, fmt.Errorf("登录二维码解码失败: %w", err)
	}
	return &wechatQRCode{uuid: envelope.UUID, image: image}, nil
}

func createWechatSession(ctx context.Context) (*wechatSession, []byte, error) {
	xLogUID := uuidUpper()
	ticket, err := fetchWxSdkTicket(ctx, xLogUID)
	if err != nil {
		return nil, nil, err
	}
	qr, err := fetchWechatQrCode(ctx, ticket)
	if err != nil {
		return nil, nil, err
	}
	return &wechatSession{xLogUID: xLogUID, sdkTicket: ticket, uuid: qr.uuid}, encodeJPEG(qr.image), nil
}

func loginWithWechatAuthCode(ctx context.Context, code, xLogUID string) (*campLoginData, error) {
	special, err := buildSpecialEncodeParam()
	if err != nil {
		return nil, err
	}

	fields := []formField{
		{"loginType", "wx"},
		{"code", code},
		{"delOldUser", "0"},
		{"key1", uuidNoDash()},
		{"lastLoginTime", "0"},
		{"lastGetRemarkTime", "0"},
	}
	fields = append(fields, buildDeviceParams()...)
	fields = append(fields, formField{"specialEncodeParam", special})

	headers := map[string]string{}
	for key, value := range campCommonHeaders {
		headers[key] = value
	}
	headers["x-log-uid"] = xLogUID
	headers["Content-Type"] = "application/x-www-form-urlencoded; charset=UTF-8"
	// 头部这组渠道参数跟 body 是同一套，但 cRand 是当场重新生成的。
	for _, field := range buildDeviceParams() {
		headers[field.key] = field.value
	}
	headers["specialEncodeParam"] = special

	status, text, err := loginDoRequest(ctx, http.MethodPost, campAPIBase+"/user/login", headers, formBody(fields))
	if err != nil {
		return nil, err
	}
	var response campLoginResponse
	_ = json.Unmarshal([]byte(text), &response)
	if !httpStatusOK(status) || !rawIsZero(response.ReturnCode) || rawText(response.Data.UserID) == "" || rawText(response.Data.Token) == "" {
		return nil, fmt.Errorf("营地登录失败（HTTP %d），稍后再试", status)
	}
	return &response.Data, nil
}

type wechatPollPayload struct {
	WxErrCode any `json:"wx_errcode"`
	ErrCode   any `json:"errcode"`
	WxCode    any `json:"wx_code"`
	Code      any `json:"code"`
}

// pollWechatQr 返回状态码、授权码与响应摘要（摘要用于去重回调）。
// 只接受数字状态码，字符串形式的状态码不参与判定（与上游 Map 的严格行为一致）。
func pollWechatQr(ctx context.Context, uuid string) (*int, string, string, error) {
	params := []formField{
		{"f", "json"},
		{"uuid", uuid},
	}
	_, text, err := loginDoRequest(ctx, http.MethodGet, wxQRCodePollAPI+"?"+formBody(params), nil, "")
	if err != nil {
		return nil, "", "", err
	}
	var payload wechatPollPayload
	_ = json.Unmarshal([]byte(text), &payload)

	statusValue := payload.WxErrCode
	if statusValue == nil {
		statusValue = payload.ErrCode
	}
	statusCode := numericValue(statusValue)

	codeValue := payload.WxCode
	if codeValue == nil {
		codeValue = payload.Code
	}
	authCode := stringValue(codeValue)

	var summary bytes.Buffer
	if compactErr := json.Compact(&summary, []byte(text)); compactErr != nil {
		summary.WriteString(text)
	}
	return statusCode, authCode, summary.String(), nil
}

// numericValue 只把 JSON number 转成整数；字符串保持不匹配。
func numericValue(value any) *int {
	number, ok := value.(float64)
	if !ok || number != float64(int(number)) {
		return nil
	}
	out := int(number)
	return &out
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return ""
	}
}

func (s *wechatSession) wait(ctx context.Context, onStatus func(string)) (*credential, error) {
	if s == nil {
		return nil, errors.New("登录会话已失效，请重新发起")
	}
	startedAt := time.Now()
	lastSummary := ""
	scannedNotified := false

	for time.Since(startedAt) < loginQRCodeTTL {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		statusCode, authCode, summary, err := pollWechatQr(ctx, s.uuid)
		if err != nil {
			return nil, err
		}

		if summary != lastSummary {
			lastSummary = summary
			if onStatus != nil && statusCode != nil {
				// 404 = 已扫码待确认，只提示一次；其它状态按响应变化提示。
				if *statusCode == 404 {
					if !scannedNotified {
						scannedNotified = true
						onStatus("404")
					}
				} else {
					onStatus(strconv.Itoa(*statusCode))
				}
			}
		}

		if authCode != "" && statusCode != nil && *statusCode == 405 {
			data, err := loginWithWechatAuthCode(ctx, authCode, s.xLogUID)
			if err != nil {
				return nil, err
			}
			return buildCredential(*data, "wx", ""), nil
		}

		if statusCode != nil {
			if message, ok := wxPollFailures[*statusCode]; ok {
				return nil, errors.New(message)
			}
		}

		if !loginSleep(ctx, loginPollInterval) {
			return nil, ctx.Err()
		}
	}
	return nil, errors.New("等待登录二维码超时，请重新发起")
}

/* ------------------------------------------------------------------ *
 * QQ 扫码（同一个浏览器会话）
 * ------------------------------------------------------------------ */

type qqSession struct {
	browser   *sdk.Browser
	stopWatch func()
	codeCh    chan string

	mu     sync.Mutex
	closed bool
}

func (s *qqSession) handleRequest(rawURL, method string) {
	if !strings.HasPrefix(rawURL, "auth://") && !strings.Contains(rawURL, "tauth.qq.com") {
		return
	}
	matched := qqCodePattern.FindStringSubmatch(rawURL)
	if matched == nil {
		return
	}
	select {
	case s.codeCh <- matched[1]:
	default:
	}
}

func (s *qqSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *qqSession) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	if s.stopWatch != nil {
		s.stopWatch()
	}
	if s.browser != nil {
		s.browser.Close()
	}
}

type qrRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

const qrRectJS = `(() => {
  const selectors = ['#qrlogin_img', '.qrlogin_img', 'img[src*="ptqrshow"]', 'img[src*="qrcode"]', 'canvas#qrlogin_canvas', '.qrlogin canvas'];
  for (const selector of selectors) {
    const el = document.querySelector(selector);
    if (el) {
      const rect = el.getBoundingClientRect();
      if (rect.width > 40 && rect.height > 40) {
        return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
      }
    }
  }
  return null;
})()`

// qrExtractJS 照上游的页面内提取逻辑：canvas 直接编码，图片先 fetch 再画到白底 canvas。
const qrExtractJS = `(async () => {
  const el = document.querySelector('#qrlogin_img, .qrlogin_img, img[src*="ptqrshow"], img[src*="qrcode"], canvas#qrlogin_canvas, .qrlogin canvas');
  if (!el) {
    return null;
  }
  try {
    const encodeImage = image => {
      const canvas = document.createElement('canvas');
      canvas.width = image.naturalWidth || image.width;
      canvas.height = image.naturalHeight || image.height;
      if (!canvas.width || !canvas.height) throw new Error('二维码图片尺寸为空');
      const ctx = canvas.getContext('2d');
      ctx.fillStyle = '#fff';
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      ctx.drawImage(image, 0, 0);
      return canvas.toDataURL('image/jpeg', 1);
    };
    if (el.tagName === 'CANVAS') return encodeImage(el);
    const src = el.currentSrc || el.src || el.getAttribute('data-src');
    if (!src) {
      return null;
    }
    const response = await fetch(src, { credentials: 'include' });
    if (!response.ok) return null;
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    try {
      const image = new Image();
      await new Promise((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error('二维码图片解码超时')), 10000);
        image.onload = () => { clearTimeout(timer); resolve(); };
        image.onerror = () => { clearTimeout(timer); reject(new Error('二维码图片解码失败')); };
        image.src = url;
      });
      return encodeImage(image);
    } finally {
      URL.revokeObjectURL(url);
    }
  } catch (error) {
    return null;
  }
})()`

const scannedJS = `(() => /扫码成功|请在手机上确认|已扫码|确认登录/.test(document.body?.innerText || ''))()`

func createQQSession(ctx context.Context) (session *qqSession, qrcode []byte, err error) {
	browser, err := sdk.OpenBrowser(ctx, sdk.BrowserOptions{
		Width:      420,
		Height:     760,
		Mobile:     true,
		ShowWindow: false,
		UserAgent:  qqQRUserAgent,
		Timeout:    qqPageTimeout,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("浏览器不可用，无法发起 QQ 扫码登录: %w", err)
	}

	s := &qqSession{
		browser: browser,
		codeCh:  make(chan string, 1),
	}
	success := false
	defer func() {
		if !success {
			s.Close()
		}
	}()

	s.stopWatch = browser.WatchURLs(s.handleRequest)

	if err := browser.Navigate(ctx, qqLoginPage); err != nil {
		return nil, nil, errors.New("打开 QQ 登录页失败，请稍后重试")
	}

	// 等页面把二维码渲染出来（在 iframe / 各版式下找一找）。
	deadline := time.Now().Add(qqQRWaitTimeout)
	var box *qrRect
	for time.Now().Before(deadline) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		var found *qrRect
		if evalErr := browser.Evaluate(ctx, qrRectJS, &found); evalErr == nil && found != nil &&
			found.Width > 40 && found.Height > 40 {
			box = found
			break
		}
		if !loginSleep(ctx, 800*time.Millisecond) {
			return nil, nil, ctx.Err()
		}
	}
	if box == nil {
		return nil, nil, errors.New("未能获取登录二维码，请稍后重试")
	}

	// 优先在页面内把二维码转成白底 JPEG。
	var dataURL *string
	_ = browser.EvaluateAwait(ctx, qrExtractJS, &dataURL)
	var imageBytes []byte
	if dataURL != nil && strings.HasPrefix(*dataURL, "data:") {
		if comma := strings.Index(*dataURL, ","); comma >= 0 {
			if decoded, decodeErr := base64.StdEncoding.DecodeString((*dataURL)[comma+1:]); decodeErr == nil {
				imageBytes = decoded
			}
		}
	}
	if len(imageBytes) == 0 {
		// 兜底：页面里读不到（版式特殊）才截图；区域截图失败再退整页。
		for _, selector := range qqQRSelectors {
			if shot, shotErr := browser.ElementScreenshot(ctx, selector); shotErr == nil && len(shot) > 0 {
				imageBytes = shot
				break
			}
		}
	}
	if len(imageBytes) == 0 {
		if shot, shotErr := browser.Screenshot(ctx); shotErr == nil && len(shot) > 0 {
			imageBytes = shot
		}
	}
	if len(imageBytes) == 0 {
		return nil, nil, errors.New("二维码截图失败，请稍后重试")
	}

	success = true
	return s, encodeJPEG(imageBytes), nil
}

func (s *qqSession) waitForCode(ctx context.Context, onStatus func(string)) (string, error) {
	began := time.Now()
	scannedNotified := false

	for time.Since(began) < qqScanTimeout {
		select {
		case code := <-s.codeCh:
			return code, nil
		default:
		}
		if s.isClosed() {
			return "", errors.New("登录会话已结束")
		}

		// 页面自己会轮询，这里只借 DOM 判断「已扫码」给用户一个提示。
		if !scannedNotified && onStatus != nil {
			var scanned bool
			if evalErr := s.browser.Evaluate(ctx, scannedJS, &scanned); evalErr == nil && scanned {
				scannedNotified = true
				onStatus("404")
			}
		}

		timer := time.NewTimer(1500 * time.Millisecond)
		select {
		case code := <-s.codeCh:
			timer.Stop()
			return code, nil
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	return "", errors.New("登录等待超时")
}

type qqTokens struct {
	AccessToken  string
	OpenID       string
	PayToken     string
	RefreshToken string
}

func exchangeCodeForTokens(ctx context.Context, code string) (*qqTokens, error) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	body, err := json.Marshal(struct {
		AppID     string `json:"appID"`
		LoginCode string `json:"loginCode"`
	}{AppID: qqAppID, LoginCode: code})
	if err != nil {
		return nil, fmt.Errorf("构造登录请求失败: %w", err)
	}
	signStr := "POST\n/cmd/QQCodeLogin\njson\nysdk\n" + timestamp + "\n" + string(body)
	mac := hmac.New(sha256.New, []byte("yyb@cloud_game:CQ8FA#"))
	mac.Write([]byte(signStr))
	digest := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	headers := map[string]string{
		"Content-Type":       "json",
		"Auth-Secret-ID":     "ysdk",
		"Auth-Secret-Digest": digest,
		"Auth-Request-Time":  timestamp,
	}
	status, text, err := loginDoRequest(ctx, http.MethodPost, "https://ysdk.qq.com/cmd/QQCodeLogin?", headers, string(body))
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Code   json.RawMessage `json:"code"`
		ErrMsg string          `json:"errmsg"`
		Data   struct {
			Ret          json.RawMessage `json:"ret"`
			ErrMsg       string          `json:"errmsg"`
			AccessToken  json.RawMessage `json:"accessToken"`
			OpenID       json.RawMessage `json:"openID"`
			PayToken     json.RawMessage `json:"payToken"`
			RefreshToken json.RawMessage `json:"refreshToken"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(text), &envelope)

	if !httpStatusOK(status) || !rawIsZero(envelope.Code) || !rawIsZero(envelope.Data.Ret) {
		message := envelope.ErrMsg
		if message == "" {
			message = envelope.Data.ErrMsg
		}
		if message == "" {
			message = fmt.Sprintf("换取登录凭证失败（%d）", status)
		}
		return nil, errors.New(message)
	}

	return &qqTokens{
		AccessToken:  rawText(envelope.Data.AccessToken),
		OpenID:       rawText(envelope.Data.OpenID),
		PayToken:     rawText(envelope.Data.PayToken),
		RefreshToken: rawText(envelope.Data.RefreshToken),
	}, nil
}

func loginCampByOpenSdk(ctx context.Context, tokens *qqTokens) (*credential, error) {
	special, err := buildSpecialEncodeParam()
	if err != nil {
		return nil, err
	}

	fields := []formField{
		{"delOldUser", "0"},
		{"key1", uuidNoDash()},
		{"lastLoginTime", "0"},
		{"lastGetRemarkTime", "0"},
		{"cChannelId", "10003391"},
		{"cClientVersionCode", "2057971306"},
		{"cClientVersionName", "10.114.0826"},
		{"cCurrentGameId", "20001"},
		{"cGameId", "20001"},
		{"cGzip", "1"},
		{"cIsArm64", "true"},
		{"cRand", strconv.FormatInt(time.Now().UnixMilli(), 10)},
		{"cSupportArm64", "true"},
		{"cSystem", "android"},
		{"cSystemVersionCode", "35"},
		{"cSystemVersionName", "15"},
		{"cpuHardware", "qcom"},
		{"gameId", "20001"},
		{"tinkerId", "2057971306_64_0"},
		{"specialEncodeParam", special},
		{"loginType", "openSdk"},
		{"accessToken", tokens.AccessToken},
		{"openId", tokens.OpenID},
		{"payToken", tokens.PayToken},
	}

	headers := map[string]string{}
	for key, value := range campCommonHeaders {
		headers[key] = value
	}
	headers["Content-Type"] = "application/x-www-form-urlencoded; charset=UTF-8"
	headers["x-log-uid"] = uuidUpper()

	status, text, err := loginDoRequest(ctx, http.MethodPost, campAPIBase+"/user/login", headers, formBody(fields))
	if err != nil {
		return nil, err
	}
	var response campLoginResponse
	_ = json.Unmarshal([]byte(text), &response)
	if !httpStatusOK(status) || !rawIsZero(response.ReturnCode) ||
		rawText(response.Data.UserID) == "" || rawText(response.Data.Token) == "" {
		if response.ReturnMsg != "" {
			return nil, errors.New(response.ReturnMsg)
		}
		return nil, fmt.Errorf("营地登录失败（%d）", status)
	}
	return buildCredential(response.Data, "qq", tokens.OpenID), nil
}

func (s *qqSession) wait(ctx context.Context, onStatus func(string)) (*credential, error) {
	if s == nil {
		return nil, errors.New("登录会话已结束")
	}
	code, err := s.waitForCode(ctx, onStatus)
	if err != nil {
		return nil, err
	}
	tokens, err := exchangeCodeForTokens(ctx, code)
	if err != nil {
		return nil, err
	}
	return loginCampByOpenSdk(ctx, tokens)
}
