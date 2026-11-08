package patina

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/jpeg"
	"image/png"
	"os"
	"strconv"

	"shizukubot/sdk"
)

const (
	noBrowserText = "没有开启puppeteer服务"
	genericError  = "处理图像时出错，请重试。"
)

func runMirage(ctx context.Context, c sdk.Call, cfg config, a *argsSet, sources []string) error {
	cover, err := prepareStaticImage(ctx, sources[0])
	if err != nil {
		return c.Reply(ctx, genericError)
	}
	inner, err := prepareStaticImage(ctx, sources[1])
	if err != nil {
		return c.Reply(ctx, genericError)
	}
	dir, err := newTempDir()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	page, err := writeAsset(dir, "mirage/mirage.html")
	if err != nil {
		return err
	}
	b, err := openBrowser(ctx, cfg)
	if err != nil {
		return c.Reply(ctx, noBrowserText)
	}
	defer b.Close()

	if err := b.Navigate(ctx, fileURL(page)); err != nil {
		return c.Reply(ctx, genericError)
	}

	fullColor := cfg.MirageFullColor
	if a.has("fullColor") {
		fullColor = true
	}
	size := a.int("size", cfg.MirageSize)
	weight := a.float("weight", cfg.MirageWeight)

	for _, script := range []string{
		setCheckboxScript("isColoredCheckbox", fullColor),
		setInputValueScript("maxSizeInput", strconv.Itoa(size)),
		setInputValueScript("innerWeightRange", strconv.FormatFloat(weight, 'f', -1, 64)),
	} {
		if _, err := evalBool(ctx, b, script); err != nil {
			return c.Reply(ctx, genericError)
		}
	}
	for _, pair := range []struct {
		id  string
		img preparedImage
	}{{"coverFileInput", cover}, {"innerFileInput", inner}} {
		if _, err := evalAwaitBool(ctx, b, injectFileScript(pair.id, pair.img)); err != nil {
			return c.Reply(ctx, genericError)
		}
	}

	if err := waitFor(ctx, b, canvasReadyScript("outputCanvas")); err != nil {
		return c.Reply(ctx, genericError)
	}
	output, err := evalString(ctx, b, canvasDataURLScript("outputCanvas"))
	if err != nil || output == "" {
		return c.Reply(ctx, genericError)
	}
	if err := replyDataURL(ctx, c, output); err != nil {
		return c.Reply(ctx, genericError)
	}
	return nil
}

func runMirageSplit(ctx context.Context, c sdk.Call, cfg config, sources []string) error {
	img, err := prepareStaticImage(ctx, sources[0])
	if err != nil {
		return c.Reply(ctx, genericError)
	}
	dir, err := newTempDir()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	page, err := writeAsset(dir, "mirage/split.html")
	if err != nil {
		return err
	}
	b, err := openBrowser(ctx, cfg)
	if err != nil {
		return c.Reply(ctx, noBrowserText)
	}
	defer b.Close()

	if err := b.Navigate(ctx, fileURL(page)); err != nil {
		return c.Reply(ctx, genericError)
	}
	if _, err := evalAwaitBool(ctx, b, injectFileScript("imageFileInput", img)); err != nil {
		return c.Reply(ctx, genericError)
	}
	condition := `(() => {
  const ok = (id) => {
    const el = document.getElementById(id);
    if (!el || !el.width || !el.height) return false;
    try { return el.getContext('2d').getImageData(0, 0, 1, 1).data[3] !== 0; } catch (e) { return false; }
  };
  return ok('black') && ok('white');
})()`
	if err := waitFor(ctx, b, condition); err != nil {
		return c.Reply(ctx, genericError)
	}
	for _, id := range []string{"black", "white"} {
		output, err := evalString(ctx, b, canvasDataURLScript(id))
		if err != nil || output == "" {
			return c.Reply(ctx, genericError)
		}
		if err := replyDataURL(ctx, c, output); err != nil {
			return c.Reply(ctx, genericError)
		}
	}
	return nil
}

func runPixelate(ctx context.Context, c sdk.Call, cfg config, a *argsSet, sources []string) error {
	img, err := prepareStaticImage(ctx, sources[0])
	if err != nil {
		return c.Reply(ctx, genericError)
	}
	template, err := asset("pixelate/pixelate.html")
	if err != nil {
		return err
	}
	html := bytes.ReplaceAll(template, []byte("__IMAGE_SRC__"), []byte(img.dataURL()))
	dir, err := newTempDir()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	page, err := writeHTML(dir, "pixelate.html", html)
	if err != nil {
		return err
	}
	b, err := openBrowser(ctx, cfg)
	if err != nil {
		return c.Reply(ctx, noBrowserText)
	}
	defer b.Close()
	if err := b.Navigate(ctx, fileURL(page)); err != nil {
		return c.Reply(ctx, genericError)
	}

	amount := a.int("pixelate", cfg.Pixelate)
	if amount < 0 {
		amount = 0
	}
	if amount > 100 {
		amount = 100
	}
	if err := waitFor(ctx, b, `document.querySelectorAll('canvas').length >= 2`); err != nil {
		return c.Reply(ctx, genericError)
	}
	slider := `(() => {
  const el = document.querySelector('.slider');
  if (!el) return false;
  el.value = ` + jsQuote(strconv.Itoa(amount)) + `;
  el.dispatchEvent(new Event('input'));
  return true;
})()`
	if _, err := evalBool(ctx, b, slider); err != nil {
		return c.Reply(ctx, genericError)
	}
	output, err := evalString(ctx, b, `(() => {
  const canvases = document.querySelectorAll('canvas');
  const target = canvases[canvases.length - 1];
  return target ? target.toDataURL('image/png') : null;
})()`)
	if err != nil || output == "" {
		return c.Reply(ctx, genericError)
	}
	if err := replyDataURL(ctx, c, output); err != nil {
		return c.Reply(ctx, genericError)
	}
	return nil
}

func runCamera(ctx context.Context, c sdk.Call, cfg config, sources []string) error {
	img, err := prepareStaticImage(ctx, sources[0])
	if err != nil {
		return c.Reply(ctx, genericError)
	}
	frame, err := asset("pics/camera.png")
	if err != nil {
		return err
	}
	template, err := asset("camera/camera.html")
	if err != nil {
		return err
	}
	fit := "cover"
	switch cfg.CameraAlignment {
	case "拉伸":
		fit = "fill"
	case "适应":
		fit = "contain"
	}
	html := bytes.ReplaceAll(template, []byte("__USER_IMAGE_SRC__"), []byte(img.dataURL()))
	html = bytes.ReplaceAll(html, []byte("__CAMERA_FRAME_SRC__"), []byte(dataURL("image/png", frame)))
	html = bytes.ReplaceAll(html, []byte("__OBJECT_FIT_STYLE__"), []byte(fit))

	dir, err := newTempDir()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	page, err := writeHTML(dir, "camera.html", html)
	if err != nil {
		return err
	}
	b, err := openBrowser(ctx, cfg)
	if err != nil {
		return c.Reply(ctx, noBrowserText)
	}
	defer b.Close()
	if err := b.Navigate(ctx, fileURL(page)); err != nil {
		return c.Reply(ctx, genericError)
	}
	if err := waitFor(ctx, b, `(() => {
  const u = document.getElementById('userImage');
  const f = document.getElementById('cameraFrame');
  return !!u && !!f && u.complete && u.naturalWidth > 0 && f.complete && f.naturalWidth > 0;
})()`); err != nil {
		return c.Reply(ctx, genericError)
	}
	shot, err := b.ElementScreenshot(ctx, "#container")
	if err != nil || len(shot) == 0 {
		return c.Reply(ctx, genericError)
	}
	output := shot
	if decoded, err := png.Decode(bytes.NewReader(shot)); err == nil {
		quality := cfg.CameraQuality
		if quality < 0 {
			quality = 0
		}
		if quality > 100 {
			quality = 100
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, decoded, &jpeg.Options{Quality: quality}); err == nil {
			output = buf.Bytes()
		}
	}
	if err := c.ReplyImageBytes(ctx, output); err != nil {
		return c.Reply(ctx, genericError)
	}
	return nil
}

func runPrism(ctx context.Context, c sdk.Call, cfg config, a *argsSet, sources []string) error {
	cover, err := prepareStaticImage(ctx, sources[0])
	if err != nil {
		return c.Reply(ctx, genericError)
	}
	inner, err := prepareStaticImage(ctx, sources[1])
	if err != nil {
		return c.Reply(ctx, genericError)
	}
	dir, err := newTempDir()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	page, err := writeAsset(dir, "gltank/gltank.html")
	if err != nil {
		return err
	}
	b, err := openBrowser(ctx, cfg)
	if err != nil {
		return c.Reply(ctx, noBrowserText)
	}
	defer b.Close()
	if err := b.Navigate(ctx, fileURL(page)); err != nil {
		return c.Reply(ctx, genericError)
	}
	if _, err := evalBool(ctx, b, `(() => { const el = document.getElementById('encodeButton'); if (!el) return false; el.click(); return true; })()`); err != nil {
		return c.Reply(ctx, genericError)
	}
	if err := waitFor(ctx, b, `!!document.getElementById('coverSourceFileInput') && !!document.getElementById('innerSourceFileInput')`); err != nil {
		return c.Reply(ctx, genericError)
	}
	for _, script := range []string{
		injectFileScript("coverSourceFileInput", cover),
		injectFileScript("innerSourceFileInput", inner),
	} {
		if _, err := evalAwaitBool(ctx, b, script); err != nil {
			return c.Reply(ctx, genericError)
		}
	}

	grayscale := !cfg.PrismFullColor
	if a.has("grayscale") {
		grayscale = true
	}
	innerThreshold := a.int("innerThreshold", cfg.PrismInnerThresh)
	coverThreshold := a.int("coverThreshold", cfg.PrismCoverThresh)
	innerContrast := a.int("innerContrast", cfg.PrismInnerContr)
	coverContrast := a.int("coverContrast", cfg.PrismCoverContr)
	size := a.int("size", cfg.PrismSize)
	reverse := a.bool("reverse", cfg.PrismReverse)
	method := a.string("method", cfg.PrismMethod)

	scripts := []string{
		setCheckboxScript("isCoverGrayCheckBox", grayscale),
		setInputValueScript("innerThresholdRange", strconv.Itoa(innerThreshold)),
		setInputValueScript("innerThresholdInput", strconv.Itoa(innerThreshold)),
		setInputValueScript("coverThresholdRange", strconv.Itoa(coverThreshold)),
		setInputValueScript("coverThresholdInput", strconv.Itoa(coverThreshold)),
		setInputValueScript("innerContrastRange", strconv.Itoa(innerContrast)),
		setInputValueScript("coverContrastRange", strconv.Itoa(coverContrast)),
		setInputValueScript("encodeSizeInput", strconv.Itoa(size)),
		setCheckboxScript("isEncodeReverseCheckBox", reverse),
		setInputValueScript("encodeMethodSelect", method),
	}
	for _, script := range scripts {
		if _, err := evalBool(ctx, b, script); err != nil {
			return c.Reply(ctx, genericError)
		}
	}
	if err := waitFor(ctx, b, canvasReadyScript("outputCanvas")); err != nil {
		return c.Reply(ctx, genericError)
	}
	output, err := evalString(ctx, b, canvasDataURLScript("outputCanvas"))
	if err != nil || output == "" {
		return c.Reply(ctx, genericError)
	}
	if err := replyDataURL(ctx, c, output); err != nil {
		return c.Reply(ctx, genericError)
	}
	return nil
}

func runPrismDecode(ctx context.Context, c sdk.Call, cfg config, a *argsSet, sources []string) error {
	img, err := prepareStaticImage(ctx, sources[0])
	if err != nil {
		return c.Reply(ctx, genericError)
	}
	dir, err := newTempDir()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	page, err := writeAsset(dir, "gltank/gltank.html")
	if err != nil {
		return err
	}
	b, err := openBrowser(ctx, cfg)
	if err != nil {
		return c.Reply(ctx, noBrowserText)
	}
	defer b.Close()
	if err := b.Navigate(ctx, fileURL(page)); err != nil {
		return c.Reply(ctx, genericError)
	}
	if _, err := evalBool(ctx, b, `(() => { const el = document.getElementById('decodeButton'); if (!el) return false; el.click(); return true; })()`); err != nil {
		return c.Reply(ctx, genericError)
	}
	if err := waitFor(ctx, b, `!!document.getElementById('optionSelect') && !!document.getElementById('decodeImageFileInput')`); err != nil {
		return c.Reply(ctx, genericError)
	}

	option := a.string("option", cfg.PrismDecodeOpt)
	if _, err := evalBool(ctx, b, setInputValueScript("optionSelect", option)); err != nil {
		return c.Reply(ctx, genericError)
	}
	if _, err := evalAwaitBool(ctx, b, injectFileScript("decodeImageFileInput", img)); err != nil {
		return c.Reply(ctx, genericError)
	}

	threshold := a.int("threshold", cfg.PrismDecodeThr)
	reverse := a.bool("reverse", false)
	scripts := []string{
		setInputValueScript("decodeThresholdRange", strconv.Itoa(threshold)),
		setInputValueScript("decodeThresholdInput", strconv.Itoa(threshold)),
		setCheckboxScript("decodeReverseInput", reverse),
	}
	if contrast := a.int("contrast", 0); a.has("contrast") {
		scripts = append(scripts, setInputValueScript("decodeContrastRange", strconv.Itoa(contrast)))
	}
	for _, script := range scripts {
		if _, err := evalBool(ctx, b, script); err != nil {
			return c.Reply(ctx, genericError)
		}
	}
	if err := waitFor(ctx, b, canvasReadyScript("decodeCanvas")); err != nil {
		return c.Reply(ctx, genericError)
	}
	output, err := evalString(ctx, b, canvasDataURLScript("decodeCanvas"))
	if err != nil || output == "" {
		return c.Reply(ctx, genericError)
	}
	if err := replyDataURL(ctx, c, output); err != nil {
		return c.Reply(ctx, genericError)
	}
	return nil
}

type apngComposeResult struct {
	Base64 string `json:"base64"`
	Error  string `json:"error"`
}

type apngExtractResult struct {
	Images []string `json:"images"`
	Error  string   `json:"error"`
}

func runAPNGCompose(ctx context.Context, c sdk.Call, cfg config, sources []string) error {
	cover, err := prepareStaticImage(ctx, sources[0])
	if err != nil {
		return c.Reply(ctx, genericError)
	}
	inner, err := prepareStaticImage(ctx, sources[1])
	if err != nil {
		return c.Reply(ctx, genericError)
	}
	dir, err := newTempDir()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	page, err := writeAsset(dir, "apng/apng.html")
	if err != nil {
		return err
	}
	b, err := openBrowser(ctx, cfg)
	if err != nil {
		return c.Reply(ctx, noBrowserText)
	}
	defer b.Close()
	if err := b.Navigate(ctx, fileURL(page)); err != nil {
		return c.Reply(ctx, "处理图片时出错："+err.Error())
	}
	setup := `(() => {
  const t = globalThis;
  t.__firstFrameDelay = ` + strconv.Itoa(cfg.APNGFirstFrameMS) + `;
  t.__background = ` + jsQuote(cfg.APNGBackground) + `;
  t.__apngResult = null;
  t.__apngError = null;
  return true;
})()`
	if _, err := evalBool(ctx, b, setup); err != nil {
		return c.Reply(ctx, "处理图片时出错："+err.Error())
	}
	for _, script := range []string{
		injectFileScript("coverFileInput", cover),
		injectFileScript("innerFileInput", inner),
	} {
		if _, err := evalAwaitBool(ctx, b, script); err != nil {
			return c.Reply(ctx, "处理图片时出错："+err.Error())
		}
	}
	if _, err := evalBool(ctx, b, `(() => { const el = document.getElementById('composeButton'); if (!el) return false; el.click(); return true; })()`); err != nil {
		return c.Reply(ctx, "处理图片时出错："+err.Error())
	}
	if err := waitFor(ctx, b, `(() => { const t = globalThis; return t.__apngResult !== null || t.__apngError !== null; })()`); err != nil {
		return c.Reply(ctx, "处理图片时出错："+err.Error())
	}
	var result apngComposeResult
	if err := b.Evaluate(ctx, `(() => { const t = globalThis; return { base64: t.__apngResult, error: t.__apngError }; })()`, &result); err != nil {
		return c.Reply(ctx, "处理图片时出错："+err.Error())
	}
	if result.Error != "" {
		return c.Reply(ctx, "处理图片时出错："+result.Error)
	}
	if result.Base64 == "" {
		return c.Reply(ctx, "处理图片时出错：APNG 生成结果为空")
	}
	data, err := decodeBase64Payload(result.Base64)
	if err != nil {
		return c.Reply(ctx, "处理图片时出错："+err.Error())
	}
	return c.ReplyImageBytes(ctx, data)
}

func runAPNGExtract(ctx context.Context, c sdk.Call, cfg config, sources []string) error {
	// APNG 必须保留原始数据，不能用第一帧代替。
	raw, err := downloadImage(ctx, sources[0])
	if err != nil {
		return c.Reply(ctx, "提取 APNG 时出错："+err.Error())
	}
	input := preparedImage{mime: "image/apng", data: raw}

	dir, err := newTempDir()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	page, err := writeAsset(dir, "apng/apng.html")
	if err != nil {
		return err
	}
	b, err := openBrowser(ctx, cfg)
	if err != nil {
		return c.Reply(ctx, noBrowserText)
	}
	defer b.Close()
	if err := b.Navigate(ctx, fileURL(page)); err != nil {
		return c.Reply(ctx, "提取 APNG 时出错："+err.Error())
	}
	if _, err := evalBool(ctx, b, `(() => { const t = globalThis; t.__extractResults = []; t.__extractError = null; return true; })()`); err != nil {
		return c.Reply(ctx, "提取 APNG 时出错："+err.Error())
	}
	if _, err := evalAwaitBool(ctx, b, injectFileScript("apngFileInput", input)); err != nil {
		return c.Reply(ctx, "提取 APNG 时出错："+err.Error())
	}
	if _, err := evalBool(ctx, b, `(() => { const el = document.getElementById('extractButton'); if (!el) return false; el.click(); return true; })()`); err != nil {
		return c.Reply(ctx, "提取 APNG 时出错："+err.Error())
	}
	if err := waitFor(ctx, b, `(() => { const t = globalThis; return (t.__extractResults && t.__extractResults.length >= 2) || t.__extractError !== null; })()`); err != nil {
		return c.Reply(ctx, "提取 APNG 时出错："+err.Error())
	}
	var result apngExtractResult
	if err := b.Evaluate(ctx, `(() => { const t = globalThis; return { images: t.__extractResults || [], error: t.__extractError }; })()`, &result); err != nil {
		return c.Reply(ctx, "提取 APNG 时出错："+err.Error())
	}
	if result.Error != "" {
		return c.Reply(ctx, "提取 APNG 时出错："+result.Error)
	}
	if len(result.Images) < 2 {
		return c.Reply(ctx, "提取 APNG 时出错：APNG 帧数量不足")
	}
	for _, image := range result.Images {
		data, err := decodeImagePayload(image)
		if err != nil {
			return c.Reply(ctx, "提取 APNG 时出错："+err.Error())
		}
		if err := c.ReplyImageBytes(ctx, data); err != nil {
			return c.Reply(ctx, "提取 APNG 时出错："+err.Error())
		}
	}
	return nil
}

func decodeBase64Payload(value string) ([]byte, error) {
	if bytes.HasPrefix([]byte(value), []byte("data:")) {
		return decodeDataURL(value)
	}
	return base64.StdEncoding.DecodeString(value)
}

func decodeImagePayload(value string) ([]byte, error) {
	return decodeBase64Payload(value)
}
