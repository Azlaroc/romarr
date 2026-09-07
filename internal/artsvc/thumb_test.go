package artsvc

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// realPNG renders an actual decodable poster-shaped PNG (the fetch tests'
// fake byte string can't exercise the thumbnail path).
func realPNG(t *testing.T, w, h int, transparent bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(0xff)
			if transparent && x < w/8 {
				a = 0 // a transparent margin, like a scan's corners
			}
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x80, A: a})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMakeThumb(t *testing.T) {
	src := realPNG(t, 800, 1067, true)
	out, err := makeThumb(src)
	if err != nil {
		t.Fatalf("makeThumb: %v", err)
	}
	if len(out) > 100<<10 {
		t.Fatalf("thumb is %dKB — want a small grid payload", len(out)>>10)
	}
	img, kind, err := image.Decode(bytes.NewReader(out))
	if err != nil || kind != "jpeg" {
		t.Fatalf("thumb decode: kind=%s err=%v", kind, err)
	}
	if img.Bounds().Dx() != thumbWidth {
		t.Fatalf("thumb width=%d, want %d", img.Bounds().Dx(), thumbWidth)
	}
	// Aspect preserved: 800x1067 → 360x480.
	if h := img.Bounds().Dy(); h < 478 || h > 482 {
		t.Fatalf("thumb height=%d, want ~480", h)
	}

	// A small source is still normalized to JPEG, never upscaled.
	small, err := makeThumb(realPNG(t, 200, 260, false))
	if err != nil {
		t.Fatalf("small: %v", err)
	}
	simg, _, _ := image.Decode(bytes.NewReader(small))
	if simg.Bounds().Dx() != 200 {
		t.Fatalf("small thumb upscaled to %d", simg.Bounds().Dx())
	}

	if _, err := makeThumb([]byte("not an image")); err == nil {
		t.Fatal("garbage must error, not emit a broken thumb")
	}
}

// A libretro hit banks BOTH files, and the thumb rendition serves the grid.
func TestBankWritesThumb(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(realPNG(t, 800, 1067, false))
	}))
	defer srv.Close()

	s, store := newTestService(t, &fakeProvider{}, srv.URL)
	item := addArtItem(t, store, "Alleyway", "gb", "/roms/gb/Alleyway (World).zip")
	if err := s.resolveOne(context.Background(), item); err != nil {
		t.Fatalf("resolveOne: %v", err)
	}
	tp, ok := s.CachedThumbPath(item)
	if !ok {
		t.Fatal("no thumb after mint")
	}
	// Absolute bound, not smaller-than-source: a synthetic gradient PNG is
	// JPEG's worst case and PNG's best, so a relative assertion would be
	// about the fixture, not the property. What the grid needs is "small".
	fi, _ := os.Stat(tp)
	if fi.Size() > 100<<10 {
		t.Fatalf("thumb is %dKB — the whole point is a small grid payload", fi.Size()>>10)
	}
}

// The campaign's re-walk retrofits thumbs for covers minted before the
// thumbnail plane existed — local work, no network (the tripwire proves it).
func TestEnsureThumbRetrofitsWithoutNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("network touched for a retrofit")
	}))
	defer srv.Close()

	s, store := newTestService(t, &fakeProvider{}, srv.URL)
	item := addArtItem(t, store, "Legacy", "gb", "/roms/gb/Legacy (World).zip")
	dir := s.titleDir("gb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Legacy (World).png"), realPNG(t, 800, 1067, false), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.CachedThumbPath(item); ok {
		t.Fatal("thumb exists before retrofit")
	}
	if err := s.resolveOne(context.Background(), item); err != nil {
		t.Fatalf("resolveOne: %v", err)
	}
	if _, ok := s.CachedThumbPath(item); !ok {
		t.Fatal("retrofit produced no thumb")
	}
}
