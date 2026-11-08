package patina

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color/palette"
	stddraw "image/draw"
	"image/gif"

	"golang.org/x/image/draw"
	"shizukubot/sdk"
)

// runTank 用 Go 的 image/gif 代替上游的 ffmpeg：两帧分别作为表图和里图，
// 首帧使用 DisposalBackground，与上游修改 GIF 头部的做法一致。
func runTank(ctx context.Context, c sdk.Call, cfg config, a *argsSet, sources []string) error {
	if err := c.Reply(ctx, "正在处理图片，请稍候..."); err != nil {
		return err
	}
	coverData, err := downloadImage(ctx, sources[0])
	if err != nil {
		return c.Reply(ctx, "处理图片时出错: "+err.Error())
	}
	innerData, err := downloadImage(ctx, sources[1])
	if err != nil {
		return c.Reply(ctx, "处理图片时出错: "+err.Error())
	}
	cover, err := decodeImage(coverData)
	if err != nil {
		return c.Reply(ctx, "无法读取图片尺寸，请重试。")
	}
	inner, err := decodeImage(innerData)
	if err != nil {
		return c.Reply(ctx, "无法读取图片尺寸，请重试。")
	}

	width := a.int("width", maxInt(cover.Bounds().Dx(), inner.Bounds().Dx()))
	height := a.int("height", maxInt(cover.Bounds().Dy(), inner.Bounds().Dy()))
	if width <= 0 || height <= 0 {
		return c.Reply(ctx, "无法读取图片尺寸，请重试。")
	}
	finalDelay := a.int("delay", cfg.TankFinalMS)
	if finalDelay <= 0 {
		finalDelay = cfg.TankFinalMS
	}

	frames := []image.Image{
		resizeImage(cover, width, height),
		resizeImage(inner, width, height),
	}
	data, err := encodeTankGIF(frames, cfg.TankLoopCount, finalDelay)
	if err != nil {
		return c.Reply(ctx, "处理图片时出错: "+err.Error())
	}
	return c.ReplyImageBytes(ctx, data)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func resizeImage(src image.Image, width, height int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

func encodeTankGIF(frames []image.Image, loopCount, finalDelayMS int) ([]byte, error) {
	if len(frames) == 0 {
		return nil, errors.New("没有可合成的帧")
	}
	bounds := frames[0].Bounds()
	animation := &gif.GIF{
		LoopCount: loopCountValue(loopCount),
		Delay:     []int{10, centiseconds(finalDelayMS)},
		Disposal:  []byte{gif.DisposalBackground, gif.DisposalNone},
	}
	for _, frame := range frames {
		paletted := image.NewPaletted(bounds, palette.Plan9)
		stddraw.FloydSteinberg.Draw(paletted, bounds, frame, frame.Bounds().Min)
		animation.Image = append(animation.Image, paletted)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, animation); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// loopCountValue 对齐 ffmpeg -loop：0 无限循环，1 只播放一次。
func loopCountValue(loop int) int {
	switch {
	case loop == 0:
		return 0
	case loop <= 1:
		return -1
	default:
		return loop - 1
	}
}

func centiseconds(ms int) int {
	cs := ms / 10
	if cs < 1 {
		cs = 1
	}
	return cs
}
