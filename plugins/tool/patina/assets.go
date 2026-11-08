package patina

import (
	"embed"
	"os"
	"path/filepath"
	"strings"
)

//go:embed html
var htmlFS embed.FS

func asset(name string) ([]byte, error) {
	return htmlFS.ReadFile("html/" + name)
}

// writeAsset 把内嵌页面写到临时目录，返回本地路径。
func writeAsset(dir, name string) (string, error) {
	data, err := asset(name)
	if err != nil {
		return "", err
	}
	return writeHTML(dir, filepath.Base(name), data)
}

func writeHTML(dir, filename string, data []byte) (string, error) {
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func newTempDir() (string, error) {
	return os.MkdirTemp("", "patina-*")
}

func fileURL(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	return "file:///" + strings.TrimPrefix(filepath.ToSlash(path), "/")
}
