package genshin

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 上游 ZeroBot-Plugin 的抽卡素材包由 FloatTech/zbpdata 分发，运行时懒加载。
// 这里走同一份资产：首次「原神十连」时下载到用户缓存目录，之后复用本地文件。
const (
	assetVersion = "0.0.103"
	assetMinSize = 10 << 20 // 低于 10 MiB 视为下载不完整
	assetMaxSize = 64 << 20
	assetRetry   = 30 * time.Minute
)

var (
	// assetMirrors 按顺序尝试，jsDelivr 的不同节点内容一致。
	assetMirrors = []string{
		"https://cdn.jsdelivr.net/gh/FloatTech/zbpdata@" + assetVersion + "/Genshin/Genshin.zip",
		"https://fastly.jsdelivr.net/gh/FloatTech/zbpdata@" + assetVersion + "/Genshin/Genshin.zip",
		"https://gcore.jsdelivr.net/gh/FloatTech/zbpdata@" + assetVersion + "/Genshin/Genshin.zip",
	}
	assetClient = &http.Client{Timeout: 3 * time.Minute}

	// assetProvider 可在测试中替换，避免真正发起下载。
	assetProvider = loadGenshinAssets

	cardNameRe = regexp.MustCompile(`_(.*)\.png$`)

	assetLoadMu sync.Mutex
	assetCached *gachaAssets
	assetErrAt  time.Time
)

// gachaAssets 是解压后的素材索引，zip 句柄保持打开供按需读取。
type gachaAssets struct {
	folders map[string][]*zip.File // 目录名 -> 该目录下的图片
	files   map[string]*zip.File   // 文件名（不含目录）-> 图片
	zr      *zip.ReadCloser
}

// gachaCard 是一张抽卡结果，含立绘及其背景、星级、元素/武器图标。
type gachaCard struct {
	File  *zip.File
	BG    *zip.File
	Star  *zip.File
	Icon  *zip.File
	Name  string
	Stars int
	Kind  string
}

// loadGenshinAssets 懒加载素材包：命中缓存直接复用，失败后冷却一段时间再重试。
func loadGenshinAssets(ctx context.Context) (*gachaAssets, error) {
	assetLoadMu.Lock()
	defer assetLoadMu.Unlock()
	if assetCached != nil {
		return assetCached, nil
	}
	if !assetErrAt.IsZero() && time.Since(assetErrAt) < assetRetry {
		return nil, errors.New("素材包暂不可用（稍后重试）")
	}
	path, err := ensureAssetZip(ctx)
	if err != nil {
		assetErrAt = time.Now()
		return nil, err
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		assetErrAt = time.Now()
		return nil, fmt.Errorf("打开素材包失败：%w", err)
	}
	assets, err := newGachaAssets(zr)
	if err != nil {
		_ = zr.Close()
		assetErrAt = time.Now()
		return nil, err
	}
	assetCached = assets
	return assets, nil
}

// ensureAssetZip 返回本地素材包路径，不存在时从镜像下载。
func ensureAssetZip(ctx context.Context) (string, error) {
	path := assetCacheFile()
	if st, err := os.Stat(path); err == nil && st.Size() >= assetMinSize {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("创建素材缓存目录失败：%w", err)
	}
	// 事件 ctx 可能带较短的截止时间，下载用独立的超时上下文。
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
	defer cancel()
	tmp := path + ".tmp"
	var lastErr error
	for _, mirror := range assetMirrors {
		if err := downloadFile(dctx, mirror, tmp); err != nil {
			lastErr = err
			continue
		}
		if err := os.Rename(tmp, path); err != nil {
			lastErr = err
			continue
		}
		return path, nil
	}
	_ = os.Remove(tmp)
	if lastErr == nil {
		lastErr = errors.New("没有可用的素材源")
	}
	return "", fmt.Errorf("下载原神素材失败：%w", lastErr)
}

func downloadFile(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := assetClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 返回状态 %s", url, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, assetMaxSize))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n < assetMinSize {
		return fmt.Errorf("%s 下载不完整（%d 字节）", url, n)
	}
	return nil
}

func assetCacheFile() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	} else {
		dir = filepath.Join(dir, "ShizukuBot")
	}
	return filepath.Join(dir, "genshin-assets-"+assetVersion+".zip")
}

// newGachaAssets 建立目录与文件名索引，并校验抽卡必需的素材都在。
func newGachaAssets(zr *zip.ReadCloser) (*gachaAssets, error) {
	a := &gachaAssets{
		folders: make(map[string][]*zip.File, 16),
		files:   make(map[string]*zip.File, 256),
		zr:      zr,
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.TrimPrefix(f.Name, "Genshin/")
		base := name[strings.LastIndex(name, "/")+1:]
		a.files[base] = f
		if i := strings.LastIndex(name, "/"); i >= 0 {
			a.folders[name[:i]] = append(a.folders[name[:i]], f)
		}
	}
	for _, dir := range []string{"five", "four", "five2", "four2", "Three"} {
		if len(a.folders[dir]) == 0 {
			return nil, fmt.Errorf("素材包缺少目录 %s", dir)
		}
	}
	for _, name := range []string{"bg0.jpg", "five_bg.jpg", "four_bg.jpg", "three_bg.jpg",
		"Reply.png", "FiveStar.png", "FourStar.png", "ThreeStar.png"} {
		if a.files[name] == nil {
			return nil, fmt.Errorf("素材包缺少 %s", name)
		}
	}
	return a, nil
}

func (a *gachaAssets) pick(dir string) *zip.File {
	fs := a.folders[dir]
	if len(fs) == 0 {
		return nil
	}
	return fs[randIntn(len(fs))]
}

func (a *gachaAssets) first(name string) *zip.File { return a.files[name] }

// drawFromAssets 按上游规则抽十连：普通池 3% 五星、17% 四星、80% 三星，
// 没有四星及以上时把最后一项换成四星；累计次数逢 9 的倍数额外保底一个五星。
func drawFromAssets(a *gachaAssets, fiveStarMode bool) ([]gachaCard, error) {
	var (
		fiveChars, fourChars   = make([]*zip.File, 0, 10), make([]*zip.File, 0, 10)
		fiveArms, fourArms     = make([]*zip.File, 0, 10), make([]*zip.File, 0, 10)
		threeArms              = make([]*zip.File, 0, 10)
		fourN, fourN2, threeN2 = 0, 0, 0
	)
	left := 10
	if atomic.LoadUint64(&totalPulls)%9 == 0 {
		if randIntn(2) == 0 {
			fiveChars = append(fiveChars, a.pick("five"))
		} else {
			fiveArms = append(fiveArms, a.pick("five2"))
		}
		left--
	}
	if fiveStarMode {
		for i := 0; i < left; i++ {
			if randIntn(2) == 0 {
				fiveChars = append(fiveChars, a.pick("five"))
			} else {
				fiveArms = append(fiveArms, a.pick("five2"))
			}
		}
	} else {
		for i := 0; i < left; i++ {
			switch x := randIntn(1000); {
			case x <= 800:
				threeN2++
				threeArms = append(threeArms, a.pick("Three"))
			case x <= 885:
				fourN++
				fourChars = append(fourChars, a.pick("four"))
			case x <= 970:
				fourN2++
				fourArms = append(fourArms, a.pick("four2"))
			case x <= 985:
				fiveChars = append(fiveChars, a.pick("five"))
			default:
				fiveArms = append(fiveArms, a.pick("five2"))
			}
		}
		if fourN+fourN2 == 0 && threeN2 > 0 {
			threeN2--
			threeArms = threeArms[:len(threeArms)-1]
			if randIntn(2) == 0 {
				fourN++
				fourChars = append(fourChars, a.pick("four"))
			} else {
				fourN2++
				fourArms = append(fourArms, a.pick("four2"))
			}
		}
		atomic.AddUint64(&totalPulls, 1)
	}
	if len(fiveChars)+len(fourChars)+len(fiveArms)+len(fourArms)+threeN2 != 10 {
		return nil, errors.New("抽卡数量异常")
	}

	type group struct {
		files []*zip.File
		stars int
		kind  string
		bg    string
		star  string
	}
	groups := []group{
		{fiveChars, 5, "角色", "five_bg.jpg", "FiveStar.png"},
		{fourChars, 4, "角色", "four_bg.jpg", "FourStar.png"},
		{fiveArms, 5, "武器", "five_bg.jpg", "FiveStar.png"},
		{fourArms, 4, "武器", "four_bg.jpg", "FourStar.png"},
		{threeArms, 3, "武器", "three_bg.jpg", "ThreeStar.png"},
	}
	cards := make([]gachaCard, 0, 10)
	for _, g := range groups {
		for _, f := range g.files {
			card, err := a.card(f, g.stars, g.kind, g.bg, g.star)
			if err != nil {
				return nil, err
			}
			cards = append(cards, card)
		}
	}
	return cards, nil
}

// card 由立绘文件推导角色/武器名与元素（武器类型）图标。
func (a *gachaAssets) card(f *zip.File, stars int, kind, bg, star string) (gachaCard, error) {
	base := f.Name[strings.LastIndex(f.Name, "/")+1:]
	m := cardNameRe.FindStringSubmatch(base)
	sep := strings.Index(base, "_")
	if m == nil || sep < 0 {
		return gachaCard{}, fmt.Errorf("无法解析素材名 %s", f.Name)
	}
	icon := a.first(base[:sep] + ".png")
	if icon == nil {
		return gachaCard{}, fmt.Errorf("缺少图标 %s.png", base[:sep])
	}
	return gachaCard{
		File:  f,
		BG:    a.first(bg),
		Star:  a.first(star),
		Icon:  icon,
		Name:  m[1],
		Stars: stars,
		Kind:  kind,
	}, nil
}

// renderImage 按上游布局把十张卡合成 1920x1080 图片：背景 + 星级底图 + 立绘 + 星级 + 元素图标。
func (a *gachaAssets) renderImage(cards []gachaCard) ([]byte, error) {
	bg0, err := decodeZipImage(a.first("bg0.jpg"))
	if err != nil {
		return nil, err
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{50, 50, 50, 255}), image.Point{}, draw.Src)
	draw.Draw(canvas, bg0.Bounds(), bg0, image.Point{}, draw.Over)

	x := 230
	for i, card := range cards {
		if i > 0 {
			x += 146
		}
		for _, f := range []*zip.File{card.BG, card.File, card.Star, card.Icon} {
			img, err := decodeZipImage(f)
			if err != nil {
				return nil, err
			}
			draw.Draw(canvas, img.Bounds().Add(image.Pt(x, 0)), img, image.Point{}, draw.Over)
		}
	}
	if f := a.first("Reply.png"); f != nil {
		if img, err := decodeZipImage(f); err == nil {
			draw.Draw(canvas, img.Bounds().Add(image.Pt(1270, 945)), img, image.Point{}, draw.Over)
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeZipImage(f *zip.File) (image.Image, error) {
	if f == nil {
		return nil, errors.New("抽卡素材缺失")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	if strings.HasSuffix(strings.ToLower(f.Name), ".jpg") {
		return jpeg.Decode(rc)
	}
	return png.Decode(rc)
}

// cardsText 汇总十连里的五星结果，没有五星时返回空字符串。
func cardsText(cards []gachaCard) string {
	var chars, weapons []string
	for _, card := range cards {
		if card.Stars != 5 {
			continue
		}
		if card.Kind == "角色" {
			chars = append(chars, card.Name)
		} else {
			weapons = append(weapons, card.Name)
		}
	}
	var b strings.Builder
	if len(chars) > 0 {
		b.WriteString("★五星角色★\n")
		b.WriteString(strings.Join(chars, " * ") + " * ")
	}
	if len(weapons) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("★五星武器★\n")
		b.WriteString(strings.Join(weapons, " * ") + " * ")
	}
	return strings.TrimSpace(b.String())
}
