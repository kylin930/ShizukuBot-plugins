package patina

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"shizukubot/sdk"
)

// browserDriver 是插件对宿主浏览器能力的最小依赖，测试时用桩替换。
type browserDriver interface {
	Navigate(context.Context, string) error
	Evaluate(context.Context, string, any) error
	EvaluateAwait(context.Context, string, any) error
	WaitVisible(context.Context, string) error
	ElementScreenshot(context.Context, string) ([]byte, error)
	Close()
}

var openBrowserDriver = func(ctx context.Context, timeout time.Duration) (browserDriver, error) {
	return sdk.OpenBrowser(ctx, sdk.BrowserOptions{Width: 1280, Height: 800, Timeout: timeout})
}

var errBrowserUnavailable = errors.New("浏览器不可用")

const waitTimeoutMS = 60000

func openBrowser(ctx context.Context, cfg config) (browserDriver, error) {
	b, err := openBrowserDriver(ctx, cfg.BrowserTimeout)
	if err != nil {
		return nil, errBrowserUnavailable
	}
	return b, nil
}

func jsQuote(s string) string {
	return strconv.Quote(s)
}

func setInputValueScript(id, value string) string {
	return fmt.Sprintf(`(() => {
  const el = document.getElementById(%s);
  if (!el) return false;
  el.value = %s;
  el.dispatchEvent(new Event('input'));
  el.dispatchEvent(new Event('change'));
  return true;
})()`, jsQuote(id), jsQuote(value))
}

func setCheckboxScript(id string, checked bool) string {
	return fmt.Sprintf(`(() => {
  const el = document.getElementById(%s);
  if (!el) return false;
  if (el.checked !== %t) el.click();
  return el.checked === %t;
})()`, jsQuote(id), checked, checked)
}

func injectFileScript(id string, img preparedImage) string {
	src := img.dataURL()
	return fmt.Sprintf(`(async () => {
  const input = document.getElementById(%s);
  if (!input) throw new Error('找不到文件输入框');
  const blob = await (await fetch(%s)).blob();
  const file = new File([blob], %s, {type: blob.type || %s});
  const dt = new DataTransfer();
  dt.items.add(file);
  input.files = dt.files;
  input.dispatchEvent(new Event('change', {bubbles: true}));
  return true;
})()`, jsQuote(id), jsQuote(src), jsQuote(img.filename()), jsQuote(img.mime))
}

func waitScript(condition string) string {
	return fmt.Sprintf(`new Promise((resolve, reject) => {
  const deadline = Date.now() + %d;
  const tick = () => {
    let ok = false;
    try { ok = (%s); } catch (e) { ok = false; }
    if (ok) return resolve(true);
    if (Date.now() > deadline) return reject(new Error('等待页面渲染超时'));
    setTimeout(tick, 100);
  };
  tick();
})`, waitTimeoutMS, condition)
}

func evalBool(ctx context.Context, b browserDriver, script string) (bool, error) {
	var out bool
	if err := b.Evaluate(ctx, script, &out); err != nil {
		return false, err
	}
	return out, nil
}

func evalString(ctx context.Context, b browserDriver, script string) (string, error) {
	var out string
	if err := b.Evaluate(ctx, script, &out); err != nil {
		return "", err
	}
	return out, nil
}

func evalAwaitBool(ctx context.Context, b browserDriver, script string) (bool, error) {
	var out bool
	if err := b.EvaluateAwait(ctx, script, &out); err != nil {
		return false, err
	}
	return out, nil
}

func waitFor(ctx context.Context, b browserDriver, condition string) error {
	var ok bool
	return b.EvaluateAwait(ctx, waitScript(condition), &ok)
}

func canvasReadyScript(id string) string {
	return fmt.Sprintf(`(() => {
  const c = document.getElementById(%s);
  if (!c || !c.width || !c.height) return false;
  try {
    const ctx = c.getContext('2d');
    const w = Math.min(c.width, 64), h = Math.min(c.height, 64);
    const d = ctx.getImageData(0, 0, w, h).data;
    for (let i = 3; i < d.length; i += 4) { if (d[i] !== 0) return true; }
    return false;
  } catch (e) { return false; }
})()`, jsQuote(id))
}

func canvasDataURLScript(id string) string {
	return fmt.Sprintf(`(() => {
  const c = document.getElementById(%s);
  return c ? c.toDataURL('image/png') : null;
})()`, jsQuote(id))
}

func replyDataURL(ctx context.Context, c sdk.Call, value string) error {
	data, err := decodeDataURL(value)
	if err != nil {
		return c.Reply(ctx, genericError)
	}
	return c.ReplyImageBytes(ctx, data)
}
