package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	gif2 "image/gif"
	"io"
	"iter"
	"log"
	"os"
	"time"
	"unsafe"

	_ "image/jpeg"
	_ "image/png"

	webp2 "github.com/gen2brain/vpx/webp"
	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/draw"
	"golang.org/x/image/math/fixed"
)

type Content struct {
	name   string
	source image.Image
	cancel func()
}

func (s *state) renderEmoji(emoji string) {
	if settings.content.name == emoji {
		return
	}

	settings.content.name = emoji
	settings.content.source = nil

	if settings.content.cancel != nil {
		settings.content.cancel()
		settings.content.cancel = nil
	}

	const size = 128

	s.srcWidth = size
	s.srcHeight = size

	s.lock.Lock()
	if s.w != uint32(size) || s.h != uint32(size) {
		s.w = uint32(size)
		s.h = uint32(size)
		s.stride = s.w * 4
	}
	s.viewWidth = s.w
	s.viewHeight = s.h

	s.lock.Unlock()

	s.viewportSetSource(
		FixedFromFloat(0), FixedFromFloat(0),
		FixedFromFloat(float64(s.srcWidth)), FixedFromFloat(float64(s.srcHeight)),
	)
	s.viewportSetDestination(s.viewWidth, s.viewHeight)

	runes := []rune(emoji)

	input := shaping.Input{
		Text:      runes,
		RunStart:  0,
		RunEnd:    len(runes),
		Direction: di.DirectionLTR,
		Face:      face2,
		Size:      fixed.I(24),
	}

	output := shaper.Shape(input)
	if len(output.Glyphs) == 0 {
		return
	}

	dst := WayImage{
		data: make([]byte, size*size*4),
		size: image.Rectangle{
			Min: image.Point{},
			Max: image.Point{X: 128, Y: 128},
		},
	}
	settings.content.source = &dst

	bitmap, found := face2.GlyphDataBitmap(output.Glyphs[0].GlyphID)
	if !found {
		log.Println("failed to get glyph data bitmap")
		return
	}

	emojiImg, _, err := image.Decode(bytes.NewReader(bitmap.Data))
	if err != nil {
		fmt.Printf("Failed to decode PNG: %v\n", err)
		return
	}

	draw.Draw(&dst, dst.Bounds(), emojiImg, emojiImg.Bounds().Min, draw.Over)

	s.drawContent()
}

func (s *state) renderImage(filepath, name string) {
	if settings.content.name == filepath {
		return
	}
	settings.content.name = ""
	settings.content.source = nil

	f, err := os.OpenFile(filepath, os.O_RDONLY, 0)
	if err != nil {
		log.Printf("failed to open image file: %v", err)
		return
	}
	defer LogFailedClose(f.Close)
	img, format, err := image.Decode(f)
	if err != nil {
		log.Printf("failed to decode image file: %v", err)
		return
	}

	if settings.content.cancel != nil {
		settings.content.cancel()
		settings.content.cancel = nil
	}

	s.srcWidth = uint32(img.Bounds().Dx())
	s.srcHeight = uint32(img.Bounds().Dy())

	s.lock.Lock()
	if s.w != uint32(img.Bounds().Dx()) || s.h != uint32(img.Bounds().Dy()) {
		s.w = uint32(img.Bounds().Dx())
		s.h = uint32(img.Bounds().Dy())
		s.stride = s.w * 4
	}
	s.viewWidth = s.w
	s.viewHeight = s.h

	s.ensureBuffer(uint32(img.Bounds().Dx()*img.Bounds().Dy()) * 4)
	s.lock.Unlock()

	s.viewportSetSource(
		FixedFromFloat(0), FixedFromFloat(0),
		FixedFromFloat(float64(s.srcWidth)), FixedFromFloat(float64(s.srcHeight)),
	)
	s.viewportSetDestination(s.viewWidth, s.viewHeight)

	if format == "webp" || format == "gif" {
		_, _ = f.Seek(0, io.SeekStart)
		if animated(s, format, name, f) {
			return
		}
	}

	settings.content.source = img
	settings.content.name = name

	//color.RGBAModel.Convert()

	s.drawContent()
}

func animated(s *state, format string, name string, f io.ReadSeekCloser) bool {
	if format == "gif" {
		return gif(s, name, f)
	}
	if format == "webp" {
		return webp(s, name, f)
	}
	return false
}

func gif(s *state, name string, f io.ReadSeekCloser) bool {
	g, err := gif2.DecodeAll(f)
	if err != nil {
		return false
	}

	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		settings.content.name = name
		settings.content.cancel = cancel

		timer := time.NewTimer(0)

		i := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				img := g.Image[i]
				s.lock.Lock()
				s.ensureBuffer(s.h * s.stride)

				if i == 0 {
					R, G, B, A := g.Image[i].Palette[g.BackgroundIndex].RGBA()
					for y := range s.h {
						for x := range s.w {
							idx := y*s.stride + x*4
							s.shmPoolData[idx+0] = uint8(B >> 8)
							s.shmPoolData[idx+1] = uint8(G >> 8)
							s.shmPoolData[idx+2] = uint8(R >> 8)
							s.shmPoolData[idx+3] = uint8(A >> 8)
						}
					}
				}

				for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
					for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
						R, G, B, A := img.At(x, y).RGBA()
						if A == 0 && !(g.Disposal != nil && g.Disposal[i] == gif2.DisposalBackground) {
							continue
						}
						idx := y*int(s.stride) + x*4
						s.shmPoolData[idx+0] = uint8(B >> 8)
						s.shmPoolData[idx+1] = uint8(G >> 8)
						s.shmPoolData[idx+2] = uint8(R >> 8)
						s.shmPoolData[idx+3] = uint8(A >> 8)
					}
				}

				if g.Disposal != nil && g.Disposal[i] != gif2.DisposalNone && i != 0 {

					for y := range outOfRage(int(s.h), img.Bounds().Min.Y, img.Bounds().Max.Y) {
						for x := range int(s.w) {
							idx := y*int(s.stride) + x*4
							s.shmPoolData[idx+3] = 0
						}
					}
					for x := range outOfRage(int(s.w), img.Bounds().Min.X, img.Bounds().Max.X) {
						for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y && y < int(s.h) && y > 0; y++ {
							idx := y*int(s.stride) + x*4
							s.shmPoolData[idx+3] = 0
						}
					}
				}

				s.lock.Unlock()
				s.waylandWLSurfaceDamage()
				s.waylandWLSurfaceCommit()

				timer.Reset(time.Duration(g.Delay[i]) * time.Millisecond * 10)

				i++
				if i >= len(g.Image) {
					i = 0
				}
			}
		}

	}()
	return true
}

func outOfRage(max int, start, end int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for y := range start {
			if !yield(y) {
				return
			}
		}
		for y := end; y < max; y++ {
			if !yield(y) {
				return
			}
		}
	}
}

func webp(s *state, name string, f io.ReadSeekCloser) bool {
	frames, err := webp2.DecodeAll(f)
	if err != nil || len(frames.Image) < 2 {
		return false
	}

	//scaledFrames := make([]image.Image, len(frames.Image))

	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		settings.content.name = name
		settings.content.cancel = cancel

		timer := time.NewTimer(0)

		i := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			if len(frames.Delay) <= i {
				timer.Stop()
			} else {
				timer.Reset(time.Duration(frames.Delay[i])*time.Millisecond - 1)
			}
			img := frames.Image[i]

			s.lock.Lock()
			//w, h := img.Bounds().Dx(), img.Bounds().Dy()
			//if s.w != uint32(w) || s.h != uint32(h) {
			//	rect := image.Rect(0, 0, int(s.w), int(s.h))
			//	if scaledFrames[i] != nil && scaledFrames[i].Bounds() == rect {
			//		img = scaledFrames[i]
			//	} else {
			//		nimg := image.NewRGBA(rect)
			//		draw.ApproxBiLinear.Scale(nimg, nimg.Bounds(), img, img.Bounds(), draw.Src, nil)
			//		img = nimg
			//		scaledFrames[i] = img
			//	}
			//}

			if uint(s.h*s.stride) < uint(s.h)*uint(s.stride) {
				panic("multiplication underflow")
			}

			//s.ensureBuffer(s.h * s.stride)
			for y := 0; y < img.Bounds().Dy() && y < int(s.h); y++ {
				for x := 0; x < img.Bounds().Dx() && x < int(s.w); x++ {
					R, G, B, A := img.At(x, y).RGBA()
					idx := y*int(s.stride) + x*4
					s.shmPoolData[idx+0] = uint8(B >> 8)
					s.shmPoolData[idx+1] = uint8(G >> 8)
					s.shmPoolData[idx+2] = uint8(R >> 8)
					s.shmPoolData[idx+3] = uint8(A >> 8)
				}
			}

			for y := img.Bounds().Dy(); y < int(s.h); y++ {
				for x := 0; x < int(s.w); x++ {
					idx := y*int(s.stride) + x*4
					s.shmPoolData[idx+3] = 0
				}
			}
			for x := img.Bounds().Dx(); x < int(s.w); x++ {
				for y := 0; y < img.Bounds().Dy(); y++ {
					idx := y*int(s.stride) + x*4
					s.shmPoolData[idx+3] = 0
				}
			}

			s.lock.Unlock()
			s.waylandWLSurfaceDamage()
			//if s.viewWidth != 0 && s.viewHeight != 0 {
			//	s.viewportSetDestination(s.viewWidth, s.viewHeight)
			//}
			s.waylandWLSurfaceCommit()

			i++
			if i >= len(frames.Image) {
				i = 0
			}

		}

	}()
	return true
}

func (s *state) drawBounds() {
	if !s.clicked {
		return
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	if int(s.h*s.stride) > len(s.shmPoolData) {
		return
	}

	red := uint32(0xffff0000)

	const width = 4

	pool := unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(s.shmPoolData))), len(s.shmPoolData)/4)

	// TOP + Bottom
	for x := uint32(0); x < s.w; x++ {
		for y := range uint32(width) {
			idx := y*s.w + x
			pool[idx] = red
		}

		for y := s.h - width; y < s.h; y++ {
			idx := y*s.w + x
			pool[idx] = red
		}
	}

	// left + right
	for y := uint32(width); y < s.h-width; y++ {
		for x := range uint32(width) {
			idx := y*s.w + x
			pool[idx] = red
		}

		for x := s.w - width; x < s.w; x++ {
			idx := y*s.w + x
			pool[idx] = red
		}
	}
}

func (s *state) drawContent() {
	img := settings.content.source
	if img == nil {
		return
	}

	//if s.w != uint32(img.Bounds().Dx()) || s.h != uint32(img.Bounds().Dy()) {
	//	fmt.Println("rescale single image")
	//	nimg := image.NewRGBA(image.Rect(0, 0, int(s.w), int(s.h)))
	//	draw.ApproxBiLinear.Scale(nimg, nimg.Bounds(), img, img.Bounds(), draw.Src, nil)
	//	img = nimg
	//}

	s.lock.Lock()
	s.ensureBuffer(s.h * s.stride)
	for y := 0; y < img.Bounds().Dy() && y < int(s.h); y++ {
		for x := 0; x < img.Bounds().Dx() && x < int(s.w); x++ {
			R, G, B, A := img.At(x, y).RGBA()
			idx := (y*int(s.stride) + x*4)
			s.shmPoolData[idx+0] = uint8(B >> 8)
			s.shmPoolData[idx+1] = uint8(G >> 8)
			s.shmPoolData[idx+2] = uint8(R >> 8)
			s.shmPoolData[idx+3] = uint8(A >> 8)
		}
	}
	s.lock.Unlock()

	s.waylandWLSurfaceDamage()
	s.waylandWLSurfaceCommit()
}

type WayImage struct {
	data []byte
	size image.Rectangle
}

func (w *WayImage) ColorModel() color.Model {
	return color.RGBAModel
}

func (w *WayImage) Bounds() image.Rectangle {
	return w.size
}

func (w *WayImage) index(x, y int) int {
	return ((y-w.size.Min.Y)*w.size.Dx() + (x - w.size.Min.X)) * 4
}

func (w *WayImage) At(x, y int) color.Color {
	if x < w.size.Min.X || y < w.size.Min.Y || x >= w.size.Max.X || y >= w.size.Max.Y {
		return color.RGBA{}
	}

	i := w.index(x, y)

	return color.RGBA{
		A: w.data[i+3],
		R: w.data[i+2],
		G: w.data[i+1],
		B: w.data[i+0],
	}
}

func (w *WayImage) Set(x, y int, c color.Color) {
	R, G, B, A := c.RGBA()
	i := w.index(x, y)
	w.data[i+3] = uint8(A >> 8)
	w.data[i+2] = uint8(R >> 8)
	w.data[i+1] = uint8(G >> 8)
	w.data[i+0] = uint8(B >> 8)
}

var face2, shaper = func() (*font.Face, shaping.HarfbuzzShaper) {
	fontPath := "/usr/share/fonts/noto/NotoColorEmoji.ttf" // TODO make user select font and for docker save in a dir

	f, err := os.Open(fontPath)
	if err != nil {
		fmt.Printf("Error reading font file: %v\n", err)
		panic(err)
	}
	defer LogFailedClose(f.Close)

	fontFace, err := font.ParseTTF(f)
	if err != nil {
		fmt.Printf("Error parsing TTF: %v\n", err)
		panic(err)
	}

	shaper := shaping.HarfbuzzShaper{}

	return fontFace, shaper
}()
