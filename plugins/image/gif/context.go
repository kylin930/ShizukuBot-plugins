package gif

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/FloatTech/gg/factory"
)

var (
	datapath string

	// materialBase 和 fontURL 便于测试替换
	materialBase = "https://cdn.jsdelivr.net/gh/FloatTech/zbp-gif-materials@master/"
	fontURL      = "https://cdn.jsdelivr.net/gh/FloatTech/zbpdata@main/Font/regular-bold.ttf"
	avatarURL    = "https://q4.qlogo.cn/g?b=qq&nk="
	httpClient   = &http.Client{Timeout: 60 * time.Second}

	fontOnce sync.Once
	fontData []byte
	fontErr  error
)

func init() {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	datapath = filepath.Join(base, "ShizukuBot", "gif") + string(os.PathSeparator)
	_ = os.MkdirAll(filepath.Join(datapath, "materials"), 0755)
	_ = os.MkdirAll(filepath.Join(datapath, "users"), 0755)
}

type context struct {
	usrdir      string
	headimgsdir []string
	selfQQ      string
}

// logoSpec 描述一张头像的来源：图片直链或 QQ 号
type logoSpec struct {
	imageURL string
	qq       string
}

func isNotExist(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

func httpGetData(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func downloadTo(url, path string) error {
	data, err := httpGetData(url)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func dlchan(name string, s *string, wg *sync.WaitGroup, exit func(error)) {
	defer wg.Done()
	target := datapath + `materials/` + name
	if isNotExist(target) {
		data, err := httpGetData(materialBase + name)
		if err != nil {
			_ = os.Remove(target)
			exit(err)
			return
		}
		if err = os.WriteFile(target, data, 0644); err != nil {
			_ = os.Remove(target)
			exit(err)
			return
		}
	}
	*s = target
}

func dlblock(name string) (string, error) {
	target := datapath + `materials/` + name
	if isNotExist(target) {
		data, err := httpGetData(materialBase + name)
		if err != nil {
			_ = os.Remove(target)
			return "", err
		}
		if err = os.WriteFile(target, data, 0644); err != nil {
			_ = os.Remove(target)
			return "", err
		}
	}
	return target, nil
}

func dlrange(prefix string, end int, wg *sync.WaitGroup, exit func(error)) []string {
	if isNotExist(datapath + `materials/` + prefix) {
		if err := os.MkdirAll(datapath+`materials/`+prefix, 0755); err != nil {
			exit(err)
			return nil
		}
	}
	c := make([]string, end)
	for i := range c {
		wg.Add(1)
		go dlchan(prefix+"/"+strconv.Itoa(i)+".png", &c[i], wg, exit)
	}
	return c
}

func newContext(user, atUser string) *context {
	c := new(context)
	c.selfQQ = user
	c.usrdir = datapath + "users/" + atUser + `/`
	_ = os.MkdirAll(c.usrdir, 0755)
	c.headimgsdir = make([]string, 2)
	c.headimgsdir[0] = datapath + "users/" + atUser + ".gif"
	c.headimgsdir[1] = datapath + "users/" + user + ".gif"
	return c
}

func (cc *context) prepareLogos(spec logoSpec) error {
	switch {
	case spec.imageURL != "":
		if err := downloadTo(spec.imageURL, cc.headimgsdir[0]); err != nil {
			return err
		}
	case spec.qq != "":
		if err := downloadTo(avatarURL+spec.qq+"&s=640", cc.headimgsdir[0]); err != nil {
			return err
		}
	default:
		return errors.New("未指定制图对象")
	}
	return downloadTo(avatarURL+cc.selfQQ+"&s=640", cc.headimgsdir[1])
}

func getBoldFont() ([]byte, error) {
	fontOnce.Do(func() {
		target := datapath + "font/regular-bold.ttf"
		if isNotExist(target) {
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				fontErr = err
				return
			}
			if err := downloadTo(fontURL, target); err != nil {
				fontErr = err
				return
			}
		}
		fontData, fontErr = os.ReadFile(target)
	})
	return fontData, fontErr
}

func loadFirstFrames(paths []string, size int) (imgs []*factory.Factory, err error) {
	imgs = make([]*factory.Factory, size)
	for i := range imgs {
		imgs[i], err = factory.LoadFirstFrame(paths[i], 0, 0)
		if err != nil {
			return nil, err
		}
	}
	return imgs, nil
}
