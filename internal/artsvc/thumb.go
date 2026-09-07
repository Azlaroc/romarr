package artsvc

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // decode source PNGs

	xdraw "golang.org/x/image/draw"
)

// The grid thumbnail: Radarr's MediaCover move. Source box art is stored as
// fetched (the detail view's copy), and a ~360px JPEG rides beside it for
// the grids — a browse paint of thirty cards is ~1MB instead of ~8MB of
// full-size PNGs crossing the Atlantic. 360px is 2x the widest grid card,
// so it stays sharp on a retina panel.
const (
	thumbWidth   = 360
	thumbQuality = 80
	// thumbSuffix keys the derived file beside its source. CachedPath's
	// exact stem+ext probes can never collide with it.
	thumbSuffix = ".thumb.jpg"
)

// thumbBG flattens transparency: near-black slate matching the card
// gradient's floor, so a scan's transparent corners blend into the tile
// instead of turning white (JPEG has no alpha).
var thumbBG = color.RGBA{R: 0x0f, G: 0x17, B: 0x2a, A: 0xff}

// makeThumb renders source image bytes into the grid thumbnail. Anything
// undecodable is an error the caller logs and lives without — a missing
// thumb degrades to serving the original, never to a broken card.
func makeThumb(src []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, fmt.Errorf("empty image")
	}

	w := b.Dx()
	h := b.Dy()
	if w > thumbWidth {
		h = h * thumbWidth / w
		if h < 1 {
			h = 1
		}
		w = thumbWidth
	}

	flat := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(flat, flat.Bounds(), &image.Uniform{C: thumbBG}, image.Point{}, draw.Src)
	// CatmullRom: the slow, good scaler — this runs once per cover at mint
	// time, never on a request path.
	xdraw.CatmullRom.Scale(flat, flat.Bounds(), img, b, xdraw.Over, nil)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, flat, &jpeg.Options{Quality: thumbQuality}); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	return out.Bytes(), nil
}
