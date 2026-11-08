package patina

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"strings"
	"testing"
	"time"

	"shizukubot/events"
	"shizukubot/sdk"
)

type stubRegistrar struct {
	commands map[string]sdk.CommandHandler
}

func (r *stubRegistrar) On(sdk.EventType, sdk.EventHandler) error { return nil }

func (r *stubRegistrar) Command(name string, handler sdk.CommandHandler) error {
	if r.commands == nil {
		r.commands = map[string]sdk.CommandHandler{}
	}
	r.commands[name] = handler
	return nil
}

type stubHost struct{ *stubRegistrar }

func (h *stubHost) Context() context.Context { return context.Background() }
func (h *stubHost) BotID() string            { return "bot" }
func (h *stubHost) Config() map[string]any   { return nil }
func (h *stubHost) State() sdk.State         { return nil }
func (h *stubHost) Log(string)               {}

func assertCommands(t *testing.T, def sdk.Definition, names ...string) {
	t.Helper()
	if err := def.Validate(); err != nil {
		t.Fatalf("definition invalid: %v", err)
	}
	registrar := &stubRegistrar{}
	if err := def.New().Load(context.Background(), &stubHost{stubRegistrar: registrar}); err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, name := range names {
		if _, ok := registrar.commands[name]; !ok {
			t.Fatalf("missing command %q in %v", name, registrar.commands)
		}
	}
}

func TestTriggerCommands(t *testing.T) {
	assertCommands(t, Definition(), "patina")
}

func encodePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 40, B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func encodeGIF(t *testing.T) []byte {
	t.Helper()
	paletted := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, paletted, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

func pngDataURL(t *testing.T) string {
	t.Helper()
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encodePNG(t, 4, 4))
}

func attachments(urls []string) json.RawMessage {
	if len(urls) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(urls))
	for _, u := range urls {
		items = append(items, map[string]any{"url": u, "content_type": "image/png", "width": 4, "height": 4})
	}
	raw, err := json.Marshal(map[string]any{"attachments": items})
	if err != nil {
		panic(err)
	}
	return raw
}

type testEnv struct {
	t       *testing.T
	botID   string
	groupID string
	userID  string
	source  events.Source
	cfg     map[string]any
	replies []sdk.Reply
}

// enabledConfig 返回六组功能全部开启的配置，测试用。
func enabledConfig() map[string]any {
	return map[string]any{
		"enableMirage":   true,
		"enablePixelate": true,
		"enableCamera":   true,
		"enablePrism":    true,
		"enableTank":     true,
		"enableAPNG":     true,
	}
}

func newEnv(t *testing.T, cfg map[string]any) *testEnv {
	t.Helper()
	merged := enabledConfig()
	for key, value := range cfg {
		merged[key] = value
	}
	return &testEnv{
		t:       t,
		botID:   "bot-" + t.Name(),
		groupID: "group-1",
		userID:  "user-1",
		source:  events.Group,
		cfg:     merged,
	}
}

func (e *testEnv) send(text string, urls ...string) {
	e.t.Helper()
	e.replies = nil
	ev := sdk.Event{
		BotID:     e.botID,
		Source:    e.source,
		GroupID:   e.groupID,
		UserID:    e.userID,
		MessageID: "msg-1",
		Message:   text,
		Raw:       attachments(urls),
		Reply: func(_ context.Context, r sdk.Reply) error {
			e.replies = append(e.replies, r)
			return nil
		},
	}
	if err := Handle(context.Background(), sdk.Call{Event: ev, Config: e.cfg}); err != nil {
		e.t.Fatalf("Handle(%q) error: %v", text, err)
	}
}

func (e *testEnv) texts() []string {
	var out []string
	for _, r := range e.replies {
		if r.Kind == "text" {
			out = append(out, r.Text)
		}
	}
	return out
}

func (e *testEnv) lastText() string {
	texts := e.texts()
	if len(texts) == 0 {
		return ""
	}
	return texts[len(texts)-1]
}

func (e *testEnv) images() []string {
	var out []string
	for _, r := range e.replies {
		if r.Kind == "image" {
			out = append(out, r.URL)
		}
	}
	return out
}

type fakeBrowser struct {
	t         *testing.T
	navigated []string
	scripts   []string
	awaits    []string
	shot      []byte
	compose   apngComposeResult
	extract   apngExtractResult
	closed    bool
}

func (f *fakeBrowser) Navigate(_ context.Context, url string) error {
	f.navigated = append(f.navigated, url)
	return nil
}

func (f *fakeBrowser) Evaluate(_ context.Context, script string, out any) error {
	f.scripts = append(f.scripts, script)
	switch v := out.(type) {
	case *bool:
		*v = true
	case *string:
		*v = pngDataURL(f.t)
	case *apngComposeResult:
		*v = f.compose
	case *apngExtractResult:
		*v = f.extract
	}
	return nil
}

func (f *fakeBrowser) EvaluateAwait(_ context.Context, script string, out any) error {
	f.awaits = append(f.awaits, script)
	if v, ok := out.(*bool); ok {
		*v = true
	}
	return nil
}

func (f *fakeBrowser) WaitVisible(context.Context, string) error { return nil }

func (f *fakeBrowser) ElementScreenshot(context.Context, string) ([]byte, error) {
	return f.shot, nil
}

func (f *fakeBrowser) Close() { f.closed = true }

func (f *fakeBrowser) hasScript(substr string) bool {
	for _, list := range [][]string{f.scripts, f.awaits} {
		for _, s := range list {
			if strings.Contains(s, substr) {
				return true
			}
		}
	}
	return false
}

// stubRuntime 替换图片下载与浏览器，返回可观测的假浏览器。
func stubRuntime(t *testing.T, shot []byte) *fakeBrowser {
	t.Helper()
	imageData := encodePNG(t, 4, 3)
	oldDownload := downloadImage
	downloadImage = func(_ context.Context, url string) ([]byte, error) {
		if strings.Contains(url, "gif") {
			return encodeGIF(t), nil
		}
		return imageData, nil
	}
	oldBrowser := openBrowserDriver
	fake := &fakeBrowser{t: t, shot: shot}
	openBrowserDriver = func(_ context.Context, _ time.Duration) (browserDriver, error) {
		return fake, nil
	}
	t.Cleanup(func() {
		downloadImage = oldDownload
		openBrowserDriver = oldBrowser
	})
	return fake
}

func TestDefinition(t *testing.T) {
	def := Definition()
	if def.Manifest.ID != "patina" {
		t.Fatalf("id = %q", def.Manifest.ID)
	}
	if err := def.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, key := range []string{"mirageName", "pixelateName", "cameraName", "prismName", "tankName", "apngName"} {
		if _, ok := def.Manifest.ConfigSchema.Properties[key]; !ok {
			t.Fatalf("missing config key %q", key)
		}
	}
}

func TestDecodeConfig(t *testing.T) {
	cfg := decodeConfig(nil)
	if cfg.EnableMirage || cfg.EnablePixelate || cfg.EnableCamera ||
		cfg.EnablePrism || cfg.EnableTank || cfg.EnableAPNG {
		t.Fatalf("六组功能应默认关闭: %+v", cfg)
	}
	if cfg.MirageName != "幻影" || cfg.MirageSize != 1200 || cfg.MirageWeight != 0.7 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.CameraAlignment != "居中填充" || cfg.CameraQuality != 50 {
		t.Fatalf("unexpected camera defaults: %+v", cfg)
	}
	if cfg.BrowserTimeout != defaultBrowserTimeout {
		t.Fatalf("timeout = %v", cfg.BrowserTimeout)
	}
	overridden := decodeConfig(map[string]any{
		"enablePixelate":    false,
		"mirageName":        " 幻影特效 ",
		"mirageSize":        float64(640),
		"cameraQuality":     "70",
		"browserTimeout":    30,
		"prismMethod":       "gap_2",
		"apngFirstFrame":    20,
		"tankLoopCount":     0,
		"mirageFullColor":   true,
		"apngBackground":    "#000000",
		"prismDecodeOption": "black",
	})
	if overridden.EnablePixelate {
		t.Fatal("enablePixelate should be false")
	}
	if overridden.MirageName != " 幻影特效 " || overridden.MirageSize != 640 {
		t.Fatalf("unexpected overrides: %+v", overridden)
	}
	if overridden.CameraQuality != 70 || overridden.BrowserTimeout != 30*time.Second {
		t.Fatalf("unexpected numeric overrides: %+v", overridden)
	}
	if overridden.PrismMethod != "gap_2" || overridden.APNGFirstFrameMS != 20 || overridden.TankLoopCount != 0 {
		t.Fatalf("unexpected enum overrides: %+v", overridden)
	}
}

func TestCandidatesOrder(t *testing.T) {
	if got := candidates(defaultConfig()); len(got) != 0 {
		t.Fatalf("默认关闭时不应有候选指令: %v", got)
	}
	list := candidates(decodeConfig(enabledConfig()))
	var names []string
	for _, c := range list {
		names = append(names, c.name)
	}
	index := func(name string) int {
		for i, n := range names {
			if n == name {
				return i
			}
		}
		return -1
	}
	if index("幻影解图") < 0 || index("幻影") < 0 {
		t.Fatalf("missing mirage names: %v", names)
	}
	if index("幻影解图") > index("幻影") {
		t.Fatalf("longer name should come first: %v", names)
	}
	if index("原图坦克2.合成") > index("原图坦克") {
		t.Fatalf("apng compose should precede tank: %v", names)
	}
}

func TestQQNumberSource(t *testing.T) {
	sources := collectSources(sdk.Call{Event: sdk.Event{}}, []string{"12345"})
	if len(sources) != 1 || sources[0] != qlogoURL("12345") {
		t.Fatalf("sources = %v", sources)
	}
	got := firstSource(sdk.Call{Event: sdk.Event{Message: "10001"}})
	if got != qlogoURL("10001") {
		t.Fatalf("firstSource = %q", got)
	}
	if got := firstSource(sdk.Call{Event: sdk.Event{Message: "不是QQ号"}}); got != "" {
		t.Fatalf("non-numeric firstSource = %q", got)
	}
}

func TestCutCommand(t *testing.T) {
	cases := []struct {
		text string
		name string
		rest string
		ok   bool
	}{
		{"幻影", "幻影", "", true},
		{"幻影  -f -s 640", "幻影", "-f -s 640", true},
		{"幻影坦克", "幻影", "", false},
	}
	for _, tc := range cases {
		rest, ok := cutCommand(tc.text, tc.name)
		if ok != tc.ok || rest != tc.rest {
			t.Fatalf("cutCommand(%q, %q) = (%q, %v)", tc.text, tc.name, rest, ok)
		}
	}
	if got := trimPrefix("patina 幻影 -f"); got != "幻影 -f" {
		t.Fatalf("trimPrefix = %q", got)
	}
	if got := trimPrefix("幻影"); got != "幻影" {
		t.Fatalf("trimPrefix = %q", got)
	}
}

func TestParseArgs(t *testing.T) {
	args := parseArgs("-f -s 640 -w 0.5 https://a/b.png -p", flagTable["mirage"])
	if len(args.positional) != 1 || args.positional[0] != "https://a/b.png" {
		t.Fatalf("positional = %v", args.positional)
	}
	set := &argsSet{flags: args.flags}
	if !set.has("fullColor") || !set.bool("fullColor", false) {
		t.Fatal("fullColor flag missing")
	}
	if set.int("size", 0) != 640 || set.float("weight", 0) != 0.5 {
		t.Fatalf("flags = %v", args.flags)
	}
	if set.has("pixelate") {
		t.Fatal("unknown flag should be ignored")
	}
	empty := &argsSet{}
	if empty.bool("reverse", true) != true || empty.int("size", 9) != 9 || empty.float("weight", 0.2) != 0.2 {
		t.Fatal("zero-value argsSet should keep defaults")
	}
	if empty.string("method", "chess") != "chess" {
		t.Fatal("zero-value argsSet string default failed")
	}
}

func TestScripts(t *testing.T) {
	img := preparedImage{mime: "image/jpeg", data: []byte{1, 2, 3}}
	inject := injectFileScript("coverFileInput", img)
	for _, want := range []string{"coverFileInput", "DataTransfer", "new File", "dispatchEvent", "input.jpg"} {
		if !strings.Contains(inject, want) {
			t.Fatalf("injectFileScript missing %q: %s", want, inject)
		}
	}
	if !strings.Contains(setCheckboxScript("isColoredCheckbox", true), "el.click()") {
		t.Fatal("setCheckboxScript should click")
	}
	if !strings.Contains(setInputValueScript("maxSizeInput", "640"), `el.value = "640"`) {
		t.Fatal("setInputValueScript value not set")
	}
	if !strings.Contains(canvasReadyScript("outputCanvas"), "outputCanvas") {
		t.Fatal("canvasReadyScript missing canvas id")
	}
	if !strings.Contains(waitScript("true"), "setTimeout") || !strings.Contains(waitScript("true"), "60000") {
		t.Fatal("waitScript should poll with timeout")
	}
	if got := img.filename(); got != "input.jpg" {
		t.Fatalf("filename = %q", got)
	}
	if got := (preparedImage{mime: "image/apng"}).filename(); got != "input.apng" {
		t.Fatalf("apng filename = %q", got)
	}
}

func TestEmbeddedAssets(t *testing.T) {
	for _, name := range []string{
		"mirage/mirage.html", "mirage/split.html", "pixelate/pixelate.html",
		"camera/camera.html", "gltank/gltank.html", "apng/apng.html", "pics/camera.png",
	} {
		data, err := asset(name)
		if err != nil || len(data) == 0 {
			t.Fatalf("asset %s: %v", name, err)
		}
	}
	mirage, _ := asset("mirage/mirage.html")
	if !bytes.Contains(mirage, []byte("coverFileInput")) || !bytes.Contains(mirage, []byte("innerFileInput")) {
		t.Fatal("mirage.html should contain both file inputs")
	}
}

func TestDetectAndPrepareImages(t *testing.T) {
	pngData := encodePNG(t, 2, 2)
	gifData := encodeGIF(t)
	if got := detectImageMime(pngData); got != "image/png" {
		t.Fatalf("png mime = %q", got)
	}
	if got := detectImageMime(gifData); got != "image/gif" {
		t.Fatalf("gif mime = %q", got)
	}
	if _, err := decodeDataURL("bad"); err == nil {
		t.Fatal("invalid data URL should fail")
	}

	oldDownload := downloadImage
	downloadImage = func(context.Context, string) ([]byte, error) { return gifData, nil }
	t.Cleanup(func() { downloadImage = oldDownload })

	img, err := prepareStaticImage(context.Background(), "http://x/a.gif")
	if err != nil {
		t.Fatalf("prepareStaticImage: %v", err)
	}
	if img.mime != "image/png" {
		t.Fatalf("gif should be converted to png, got %q", img.mime)
	}
	if _, err := png.Decode(bytes.NewReader(img.data)); err != nil {
		t.Fatalf("converted gif is not png: %v", err)
	}
}

func TestHandleSkipsUnsupportedSource(t *testing.T) {
	env := newEnv(t, nil)
	env.source = events.Source("sandbox")
	env.send("幻影", "http://x/a.png", "http://x/b.png")
	if len(env.replies) != 0 {
		t.Fatalf("unexpected replies: %+v", env.replies)
	}
}

func TestHandleIgnoresUnknownCommand(t *testing.T) {
	env := newEnv(t, nil)
	env.send("幻影坦克", "http://x/a.png", "http://x/b.png")
	if len(env.replies) != 0 {
		t.Fatalf("unexpected replies: %+v", env.replies)
	}
}

func TestPendingFlow(t *testing.T) {
	stubRuntime(t, encodePNG(t, 5, 5))
	env := newEnv(t, nil)
	env.send("幻影")
	if got := env.lastText(); got != "请发送一张图片作为【表图】：" {
		t.Fatalf("first prompt = %q", got)
	}
	env.send("", "http://x/cover.png")
	if got := env.lastText(); got != "请发送一张图片作为【里图】：" {
		t.Fatalf("second prompt = %q", got)
	}
	if len(env.images()) != 0 {
		t.Fatal("should not render before both images arrive")
	}
}

func TestPendingRequiresImage(t *testing.T) {
	env := newEnv(t, nil)
	env.send("幻影")
	env.send("纯文本")
	if got := env.lastText(); got != "未检测到有效的图片，请重试。" {
		t.Fatalf("missing image text = %q", got)
	}
}

func TestRunMirage(t *testing.T) {
	fake := stubRuntime(t, nil)
	env := newEnv(t, map[string]any{"mirageFullColor": false, "mirageSize": 800, "mirageWeight": 0.35})
	env.send("幻影 -f -s 640 -w 0.5", "http://x/cover.png", "http://x/inner.png")

	images := env.images()
	if len(images) != 1 || !strings.HasPrefix(images[0], "data:image/png;base64,") {
		t.Fatalf("images = %v", images)
	}
	if len(fake.navigated) != 1 || !strings.HasPrefix(fake.navigated[0], "file:///") {
		t.Fatalf("navigated = %v", fake.navigated)
	}
	for _, want := range []string{"isColoredCheckbox", `el.value = "640"`, `el.value = "0.5"`, "coverFileInput", "innerFileInput"} {
		if !fake.hasScript(want) {
			t.Fatalf("missing script %q", want)
		}
	}
	if !fake.closed {
		t.Fatal("browser should be closed")
	}
}

func TestRunMirageSplit(t *testing.T) {
	stubRuntime(t, nil)
	env := newEnv(t, nil)
	env.send("幻影解图", "http://x/mirage.png")
	if images := env.images(); len(images) != 2 {
		t.Fatalf("split should reply two images, got %d", len(images))
	}
}

func TestRunPixelateClampsAmount(t *testing.T) {
	fake := stubRuntime(t, nil)
	env := newEnv(t, nil)
	env.send("像素化 -p 150", "http://x/a.png")
	if len(env.images()) != 1 {
		t.Fatalf("images = %v", env.images())
	}
	if !fake.hasScript(`el.value = "100"`) {
		t.Fatal("pixelate amount should be clamped to 100")
	}
}

func TestRunCameraEncodesJPEG(t *testing.T) {
	stubRuntime(t, encodePNG(t, 6, 8))
	env := newEnv(t, map[string]any{"cameraAlignment": "拉伸", "cameraQuality": 60})
	env.send("相机镜框", "http://x/a.png")
	images := env.images()
	if len(images) != 1 || !strings.HasPrefix(images[0], "data:image/jpeg;base64,") {
		t.Fatalf("camera should reply jpeg, got %v", images)
	}
}

func TestRunPrismFlags(t *testing.T) {
	fake := stubRuntime(t, nil)
	env := newEnv(t, nil)
	env.send("光棱 -g -it 10 -ct 20 -ic 30 -cc 40 -s 500 -r -m gap_2", "http://x/cover.png", "http://x/inner.png")
	if len(env.images()) != 1 {
		t.Fatalf("images = %v", env.images())
	}
	for _, want := range []string{
		"coverSourceFileInput", "innerSourceFileInput",
		`el.value = "10"`, `el.value = "20"`, `el.value = "30"`, `el.value = "40"`,
		`el.value = "500"`, `el.value = "gap_2"`,
		"isCoverGrayCheckBox", "isEncodeReverseCheckBox",
	} {
		if !fake.hasScript(want) {
			t.Fatalf("missing script %q", want)
		}
	}
	if !strings.Contains(strings.Join(fake.scripts, "\n"), "el.checked !== true") {
		t.Fatal("grayscale/reverse checkboxes should be checked")
	}
}

func TestRunPrismDecodeFlags(t *testing.T) {
	fake := stubRuntime(t, nil)
	env := newEnv(t, nil)
	env.send("光棱取图 -t 200 -o black -r --contrast 30", "http://x/a.png")
	if len(env.images()) != 1 {
		t.Fatalf("images = %v", env.images())
	}
	for _, want := range []string{
		"decodeImageFileInput", `el.value = "200"`, `el.value = "black"`, `el.value = "30"`,
		"decodeReverseInput", "decodeContrastRange",
	} {
		if !fake.hasScript(want) {
			t.Fatalf("missing script %q", want)
		}
	}
}

func TestRunTankProducesTwoFrameGIF(t *testing.T) {
	stubRuntime(t, nil)
	env := newEnv(t, map[string]any{"tankLoopCount": 1, "tankFinalDelay": 50000})
	env.send("原图坦克 -w 6 -h 5", "http://x/cover.png", "http://x/inner.png")

	texts := env.texts()
	if len(texts) != 1 || texts[0] != "正在处理图片，请稍候..." {
		t.Fatalf("texts = %v", texts)
	}
	images := env.images()
	if len(images) != 1 {
		t.Fatalf("images = %v", images)
	}
	raw, err := decodeDataURL(images[0])
	if err != nil {
		t.Fatalf("decode gif: %v", err)
	}
	animation, err := gif.DecodeAll(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gif decode: %v", err)
	}
	if len(animation.Image) != 2 {
		t.Fatalf("frames = %d", len(animation.Image))
	}
	if animation.LoopCount != -1 {
		t.Fatalf("loop = %d", animation.LoopCount)
	}
	if len(animation.Delay) < 2 || animation.Delay[0] != 10 || animation.Delay[1] != 5000 {
		t.Fatalf("delay = %v", animation.Delay)
	}
	if len(animation.Disposal) < 2 || animation.Disposal[0] != gif.DisposalBackground {
		t.Fatalf("disposal = %v", animation.Disposal)
	}
	if got := animation.Image[0].Bounds(); got.Dx() != 6 || got.Dy() != 5 {
		t.Fatalf("frame size = %v", got)
	}
}

func TestRunTankRejectsBadImage(t *testing.T) {
	oldDownload := downloadImage
	downloadImage = func(context.Context, string) ([]byte, error) { return []byte("not-image"), nil }
	t.Cleanup(func() { downloadImage = oldDownload })

	env := newEnv(t, nil)
	env.send("原图坦克", "http://x/a.png", "http://x/b.png")
	if got := env.lastText(); got != "无法读取图片尺寸，请重试。" {
		t.Fatalf("text = %q", got)
	}
}

func TestRunAPNGCompose(t *testing.T) {
	fake := stubRuntime(t, nil)
	fake.compose = apngComposeResult{
		Base64: base64.StdEncoding.EncodeToString(encodePNG(t, 3, 3)),
	}
	env := newEnv(t, map[string]any{"apngFirstFrame": 20, "apngBackground": "#000000"})
	env.send("原图坦克2.合成", "http://x/cover.png", "http://x/inner.png")

	if images := env.images(); len(images) != 1 || !strings.HasPrefix(images[0], "data:image/png;base64,") {
		t.Fatalf("images = %v", images)
	}
	for _, want := range []string{"__firstFrameDelay = 20", `__background = "#000000"`, "coverFileInput", "composeButton"} {
		if !fake.hasScript(want) {
			t.Fatalf("missing script %q", want)
		}
	}
}

func TestRunAPNGComposeReportsPageError(t *testing.T) {
	fake := stubRuntime(t, nil)
	fake.compose = apngComposeResult{Error: "帧尺寸不一致"}
	env := newEnv(t, nil)
	env.send("原图坦克2.合成", "http://x/cover.png", "http://x/inner.png")
	if got := env.lastText(); got != "处理图片时出错：帧尺寸不一致" {
		t.Fatalf("text = %q", got)
	}
}

func TestRunAPNGExtract(t *testing.T) {
	fake := stubRuntime(t, nil)
	fake.extract = apngExtractResult{
		Images: []string{pngDataURL(t), pngDataURL(t)},
	}
	env := newEnv(t, nil)
	env.send("原图坦克2.提取", "http://x/anim.png")
	if images := env.images(); len(images) != 2 {
		t.Fatalf("extract should reply two images, got %v", images)
	}
	if !fake.hasScript("apngFileInput") || !fake.hasScript("extractButton") {
		t.Fatalf("scripts = %v", fake.scripts)
	}
}

func TestRunAPNGExtractInsufficientFrames(t *testing.T) {
	fake := stubRuntime(t, nil)
	fake.extract = apngExtractResult{Images: []string{pngDataURL(t)}}
	env := newEnv(t, nil)
	env.send("原图坦克2.提取", "http://x/anim.png")
	if got := env.lastText(); got != "提取 APNG 时出错：APNG 帧数量不足" {
		t.Fatalf("text = %q", got)
	}
}

func TestBrowserUnavailable(t *testing.T) {
	oldDownload := downloadImage
	downloadImage = func(context.Context, string) ([]byte, error) { return encodePNG(t, 2, 2), nil }
	oldBrowser := openBrowserDriver
	openBrowserDriver = func(context.Context, time.Duration) (browserDriver, error) {
		return nil, errBrowserUnavailable
	}
	t.Cleanup(func() {
		downloadImage = oldDownload
		openBrowserDriver = oldBrowser
	})

	env := newEnv(t, nil)
	env.send("像素化", "http://x/a.png")
	if got := env.lastText(); got != noBrowserText {
		t.Fatalf("text = %q", got)
	}
}

func TestDownloadErrorRepliesGeneric(t *testing.T) {
	oldDownload := downloadImage
	downloadImage = func(context.Context, string) ([]byte, error) { return nil, errBrowserUnavailable }
	t.Cleanup(func() { downloadImage = oldDownload })

	env := newEnv(t, nil)
	env.send("幻影解图", "http://x/a.png")
	if got := env.lastText(); got != genericError {
		t.Fatalf("text = %q", got)
	}
}

func TestEncodeTankGIFLoopSemantics(t *testing.T) {
	frames := []image.Image{
		image.NewRGBA(image.Rect(0, 0, 2, 2)),
		image.NewRGBA(image.Rect(0, 0, 2, 2)),
	}
	cases := []struct {
		loop int
		want int
	}{{0, 0}, {1, -1}, {3, 2}}
	for _, tc := range cases {
		data, err := encodeTankGIF(frames, tc.loop, 1000)
		if err != nil {
			t.Fatalf("encode loop %d: %v", tc.loop, err)
		}
		animation, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("decode loop %d: %v", tc.loop, err)
		}
		if animation.LoopCount != tc.want {
			t.Fatalf("loop %d -> %d, want %d", tc.loop, animation.LoopCount, tc.want)
		}
	}
	if got := loopCountValue(5); got != 4 {
		t.Fatalf("loopCountValue(5) = %d", got)
	}
	if got := centiseconds(4); got != 1 {
		t.Fatalf("centiseconds(4) = %d", got)
	}
}
