package main

import (
	"context"
	"image"
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
)

type Content struct {
	name   string
	source image.Image
	cancel func()
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

	color := uint32(0xffff0000)

	const width = 4

	pool := unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(s.shmPoolData))), len(s.shmPoolData)/4)

	// TOP + Bottom
	for x := uint32(0); x < s.w; x++ {
		for y := range uint32(width) {
			idx := y*s.w + x
			pool[idx] = color
		}

		for y := s.h - width; y < s.h; y++ {
			idx := y*s.w + x
			pool[idx] = color
		}
	}

	// left + right
	for y := uint32(width); y < s.h-width; y++ {
		for x := range uint32(width) {
			idx := y*s.w + x
			pool[idx] = color
		}

		for x := s.w - width; x < s.w; x++ {
			idx := y*s.w + x
			pool[idx] = color
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
