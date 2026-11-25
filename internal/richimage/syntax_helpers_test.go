package richimage

import (
	"context"
	"image/color"
)

// colorsOf is codeColors with a background context, for tests that do not
// exercise cancellation.
func colorsOf(th theme, info, code string) []color.RGBA {
	colors, err := codeColors(context.Background(), th, info, code)
	if err != nil {
		panic(err)
	}
	return colors
}
