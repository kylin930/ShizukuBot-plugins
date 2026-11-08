package gif

import (
	"image"

	"github.com/FloatTech/gg/factory"
)

func (cc *context) getLogo(w int, h int) (*image.NRGBA, error) {
	frame, err := factory.LoadFirstFrame(cc.headimgsdir[0], w, h)
	if err != nil {
		return nil, err
	}
	return frame.Circle(0).Image(), nil
}

func (cc *context) getLogo2(w int, h int) (*image.NRGBA, error) {
	frame, err := factory.LoadFirstFrame(cc.headimgsdir[1], w, h)
	if err != nil {
		return nil, err
	}
	return frame.Circle(0).Image(), nil
}
