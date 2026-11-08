package wife

import "embed"

//go:embed assets
var assetsFS embed.FS

func asset(name string) ([]byte, error) {
	return assetsFS.ReadFile("assets/" + name)
}
