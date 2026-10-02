package main

import (
	"image"
	"image/color"
)

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
	return w.PixOffset(x, y)
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

func (w WayImage) PixOffset(x, y int) int {
	return (y-w.size.Min.Y)*(w.size.Dx()*4) + (x-w.size.Min.X)*4
}

// DrawMask aligns r.Min in dst with sp in src and mp in mask and then replaces the rectangle r
// in dst with the result of a Porter-Duff composition. A nil mask is treated as opaque.
func DrawMask(dst *WayImage, r image.Rectangle, src image.Image, sp image.Point) {
	clip(dst, &r, src, &sp)
	if r.Empty() {
		return
	}

	// Fast paths for special cases. If none of them apply, then we fall back
	// to general but slower implementations.
	//
	// For NRGBA and NRGBA64 image types, the code paths aren't just faster.
	// They also avoid the information loss that would otherwise occur from
	// converting non-alpha-premultiplied color to and from alpha-premultiplied
	// color. See TestDrawSrcNonpremultiplied.

	switch src0 := src.(type) {
	case *image.Uniform:
		sr, sg, sb, sa := src0.RGBA()
		drawFillSrc(dst, r, sr, sg, sb, sa)
		return
	case *image.RGBA:
	case *image.NRGBA:
		drawNRGBASrc(dst, r, src0, sp)
		return
	case *image.YCbCr:
		if DrawYCbCr(dst, r, src0, sp) {
			return
		}
	case *image.Gray:
		drawGray(dst, r, src0, sp)
		return
	case *image.CMYK:
		drawCMYK(dst, r, src0, sp)
		return
	}

	drawRGBA(dst, r, src, sp)
}

// clip clips r against each image's bounds (after translating into the
// destination image's coordinate space) and shifts the points sp and mp by
// the same amount as the change in r.Min.
func clip(dst image.Image, r *image.Rectangle, src image.Image, sp *image.Point) {
	orig := r.Min
	*r = r.Intersect(dst.Bounds())
	*r = r.Intersect(src.Bounds().Add(orig.Sub(*sp)))
	dx := r.Min.X - orig.X
	dy := r.Min.Y - orig.Y
	if dx == 0 && dy == 0 {
		return
	}
	sp.X += dx
	sp.Y += dy
}

// DrawYCbCr draws the YCbCr source image on the RGBA destination image with
// r.Min in dst aligned with sp in src. It reports whether the draw was
// successful. If it returns false, no dst pixels were changed.
//
// This function assumes that r is entirely within dst's bounds and the
// translation of r from dst coordinate space to src coordinate space is
// entirely within src's bounds.
func DrawYCbCr(dst *WayImage, r image.Rectangle, src *image.YCbCr, sp image.Point) (ok bool) {
	// This function exists in the image/internal/imageutil package because it
	// is needed by both the image/draw and image/jpeg packages, but it doesn't
	// seem right for one of those two to depend on the other.
	//
	// Another option is to have this code be exported in the image package,
	// but we'd need to make sure we're totally happy with the API (for the
	// rest of Go 1 compatibility), and decide if we want to have a more
	// general purpose DrawToRGBA method for other image types. One possibility
	// is:
	//
	// func (src *YCbCr) CopyToRGBA(dst *RGBA, dr, sr Rectangle) (effectiveDr, effectiveSr Rectangle)
	//
	// in the spirit of the built-in copy function for 1-dimensional slices,
	// that also allowed a CopyFromRGBA method if needed.

	x0 := (r.Min.X - dst.size.Min.X) * 4
	x1 := (r.Max.X - dst.size.Min.X) * 4
	y0 := r.Min.Y - dst.size.Min.Y
	y1 := r.Max.Y - dst.size.Min.X

	stride := r.Dx() * 4

	switch src.SubsampleRatio {
	case image.YCbCrSubsampleRatio444:
		for y, sy := y0, sp.Y; y != y1; y, sy = y+1, sy+1 {
			dpix := dst.data[y*stride:]
			yi := (sy-src.Rect.Min.Y)*src.YStride + (sp.X - src.Rect.Min.X)

			ci := (sy-src.Rect.Min.Y)*src.CStride + (sp.X - src.Rect.Min.X)
			for x := x0; x != x1; x, yi, ci = x+4, yi+1, ci+1 {

				// This is an inline version of image/color/ycbcr.go's func YCbCrToRGB.
				yy1 := int32(src.Y[yi]) * 0x10101
				cb1 := int32(src.Cb[ci]) - 128
				cr1 := int32(src.Cr[ci]) - 128

				// The bit twiddling below is equivalent to
				//
				// r := (yy1 + 91881*cr1) >> 16
				// if r < 0 {
				//     r = 0
				// } else if r > 0xff {
				//     r = ^int32(0)
				// }
				//
				// but uses fewer branches and is faster.
				// Note that the uint8 type conversion in the return
				// statement will convert ^int32(0) to 0xff.
				// The code below to compute g and b uses a similar pattern.
				r := yy1 + 91881*cr1
				if uint32(r)&0xff000000 == 0 {
					r >>= 16
				} else {
					r = ^(r >> 31)
				}

				g := yy1 - 22554*cb1 - 46802*cr1
				if uint32(g)&0xff000000 == 0 {
					g >>= 16
				} else {
					g = ^(g >> 31)
				}

				b := yy1 + 116130*cb1
				if uint32(b)&0xff000000 == 0 {
					b >>= 16
				} else {
					b = ^(b >> 31)
				}

				// use a temp slice to hint to the compiler that a single bounds check suffices
				rgba := dpix[x : x+4 : len(dpix)]
				rgba[0] = uint8(b)
				rgba[1] = uint8(g)
				rgba[2] = uint8(r)
				rgba[3] = 255
			}
		}

	case image.YCbCrSubsampleRatio422:
		for y, sy := y0, sp.Y; y != y1; y, sy = y+1, sy+1 {
			dpix := dst.data[y*stride:]
			yi := (sy-src.Rect.Min.Y)*src.YStride + (sp.X - src.Rect.Min.X)

			ciBase := (sy-src.Rect.Min.Y)*src.CStride - src.Rect.Min.X/2
			for x, sx := x0, sp.X; x != x1; x, sx, yi = x+4, sx+1, yi+1 {
				ci := ciBase + sx/2

				// This is an inline version of image/color/ycbcr.go's func YCbCrToRGB.
				yy1 := int32(src.Y[yi]) * 0x10101
				cb1 := int32(src.Cb[ci]) - 128
				cr1 := int32(src.Cr[ci]) - 128

				// The bit twiddling below is equivalent to
				//
				// r := (yy1 + 91881*cr1) >> 16
				// if r < 0 {
				//     r = 0
				// } else if r > 0xff {
				//     r = ^int32(0)
				// }
				//
				// but uses fewer branches and is faster.
				// Note that the uint8 type conversion in the return
				// statement will convert ^int32(0) to 0xff.
				// The code below to compute g and b uses a similar pattern.
				r := yy1 + 91881*cr1
				if uint32(r)&0xff000000 == 0 {
					r >>= 16
				} else {
					r = ^(r >> 31)
				}

				g := yy1 - 22554*cb1 - 46802*cr1
				if uint32(g)&0xff000000 == 0 {
					g >>= 16
				} else {
					g = ^(g >> 31)
				}

				b := yy1 + 116130*cb1
				if uint32(b)&0xff000000 == 0 {
					b >>= 16
				} else {
					b = ^(b >> 31)
				}

				// use a temp slice to hint to the compiler that a single bounds check suffices
				rgba := dpix[x : x+4 : len(dpix)]
				rgba[0] = uint8(b)
				rgba[1] = uint8(g)
				rgba[2] = uint8(r)
				rgba[3] = 255
			}
		}

	case image.YCbCrSubsampleRatio420:
		for y, sy := y0, sp.Y; y != y1; y, sy = y+1, sy+1 {
			dpix := dst.data[y*stride:]
			yi := (sy-src.Rect.Min.Y)*src.YStride + (sp.X - src.Rect.Min.X)

			ciBase := (sy/2-src.Rect.Min.Y/2)*src.CStride - src.Rect.Min.X/2
			for x, sx := x0, sp.X; x != x1; x, sx, yi = x+4, sx+1, yi+1 {
				ci := ciBase + sx/2

				// This is an inline version of image/color/ycbcr.go's func YCbCrToRGB.
				yy1 := int32(src.Y[yi]) * 0x10101
				cb1 := int32(src.Cb[ci]) - 128
				cr1 := int32(src.Cr[ci]) - 128

				// The bit twiddling below is equivalent to
				//
				// r := (yy1 + 91881*cr1) >> 16
				// if r < 0 {
				//     r = 0
				// } else if r > 0xff {
				//     r = ^int32(0)
				// }
				//
				// but uses fewer branches and is faster.
				// Note that the uint8 type conversion in the return
				// statement will convert ^int32(0) to 0xff.
				// The code below to compute g and b uses a similar pattern.
				r := yy1 + 91881*cr1
				if uint32(r)&0xff000000 == 0 {
					r >>= 16
				} else {
					r = ^(r >> 31)
				}

				g := yy1 - 22554*cb1 - 46802*cr1
				if uint32(g)&0xff000000 == 0 {
					g >>= 16
				} else {
					g = ^(g >> 31)
				}

				b := yy1 + 116130*cb1
				if uint32(b)&0xff000000 == 0 {
					b >>= 16
				} else {
					b = ^(b >> 31)
				}

				// use a temp slice to hint to the compiler that a single bounds check suffices
				rgba := dpix[x : x+4 : len(dpix)]
				rgba[0] = uint8(b)
				rgba[1] = uint8(g)
				rgba[2] = uint8(r)
				rgba[3] = 255
			}
		}

	case image.YCbCrSubsampleRatio440:
		for y, sy := y0, sp.Y; y != y1; y, sy = y+1, sy+1 {
			dpix := dst.data[y*stride:]
			yi := (sy-src.Rect.Min.Y)*src.YStride + (sp.X - src.Rect.Min.X)

			ci := (sy/2-src.Rect.Min.Y/2)*src.CStride + (sp.X - src.Rect.Min.X)
			for x := x0; x != x1; x, yi, ci = x+4, yi+1, ci+1 {

				// This is an inline version of image/color/ycbcr.go's func YCbCrToRGB.
				yy1 := int32(src.Y[yi]) * 0x10101
				cb1 := int32(src.Cb[ci]) - 128
				cr1 := int32(src.Cr[ci]) - 128

				// The bit twiddling below is equivalent to
				//
				// r := (yy1 + 91881*cr1) >> 16
				// if r < 0 {
				//     r = 0
				// } else if r > 0xff {
				//     r = ^int32(0)
				// }
				//
				// but uses fewer branches and is faster.
				// Note that the uint8 type conversion in the return
				// statement will convert ^int32(0) to 0xff.
				// The code below to compute g and b uses a similar pattern.
				r := yy1 + 91881*cr1
				if uint32(r)&0xff000000 == 0 {
					r >>= 16
				} else {
					r = ^(r >> 31)
				}

				g := yy1 - 22554*cb1 - 46802*cr1
				if uint32(g)&0xff000000 == 0 {
					g >>= 16
				} else {
					g = ^(g >> 31)
				}

				b := yy1 + 116130*cb1
				if uint32(b)&0xff000000 == 0 {
					b >>= 16
				} else {
					b = ^(b >> 31)
				}

				// use a temp slice to hint to the compiler that a single bounds check suffices
				rgba := dpix[x : x+4 : len(dpix)]
				rgba[0] = uint8(b)
				rgba[1] = uint8(g)
				rgba[2] = uint8(r)
				rgba[3] = 255
			}
		}

	default:
		return false
	}
	return true
}

func drawNRGBASrc(dst *WayImage, r image.Rectangle, src *image.NRGBA, sp image.Point) {
	i0 := (r.Min.X - dst.size.Min.X) * 4
	i1 := (r.Max.X - dst.size.Min.X) * 4
	si0 := (sp.X - src.Rect.Min.X) * 4
	yMax := r.Max.Y - dst.size.Min.Y

	y := r.Min.Y - dst.size.Min.Y
	sy := sp.Y - src.Rect.Min.Y

	stride := dst.size.Dx() * 4

	for ; y != yMax; y, sy = y+1, sy+1 {
		dpix := dst.data[y*stride:]
		spix := src.Pix[sy*src.Stride:]

		for i, si := i0, si0; i < i1; i, si = i+4, si+4 {
			// Convert from non-premultiplied color to pre-multiplied color.
			s := spix[si : si+4 : si+4] // Small cap improves performance, see https://golang.org/issue/27857
			sa := uint32(s[3]) * 0x101
			sr := uint32(s[0]) * sa / 0xff
			sg := uint32(s[1]) * sa / 0xff
			sb := uint32(s[2]) * sa / 0xff

			d := dpix[i : i+4 : i+4] // Small cap improves performance, see https://golang.org/issue/27857
			d[0] = uint8(sb >> 8)
			d[1] = uint8(sg >> 8)
			d[2] = uint8(sr >> 8)
			d[3] = uint8(sa >> 8)
		}
	}
}

const m = 1<<16 - 1

func drawRGBA(dst *WayImage, r image.Rectangle, src image.Image, sp image.Point) {
	x0, x1, dx := r.Min.X, r.Max.X, 1
	y0, y1, dy := r.Min.Y, r.Max.Y, 1
	if image.Image(dst) == src && r.Overlaps(r.Add(sp.Sub(r.Min))) {
		if sp.Y < r.Min.Y || sp.Y == r.Min.Y && sp.X < r.Min.X {
			x0, x1, dx = x1-1, x0-1, -1
			y0, y1, dy = y1-1, y0-1, -1
		}
	}

	sy := sp.Y + y0 - r.Min.Y
	sx0 := sp.X + x0 - r.Min.X
	sx1 := sx0 + (x1 - x0)
	i0 := dst.PixOffset(x0, y0)
	di := dx * 4

	stride := dst.size.Dx() * 4

	// Try the image.RGBA64Image interface, part of the standard library since
	// Go 1.17.
	//
	// This optimization is similar to how FALLBACK1.17 optimizes FALLBACK1.0
	// in DrawMask, except here the concrete type of dst is known to be
	// *image.RGBA.
	if src0, _ := src.(image.RGBA64Image); src0 != nil {

		for y := y0; y != y1; y, sy = y+dy, sy+dy {
			for i, sx := i0, sx0; sx != sx1; i, sx = i+di, sx+dx {
				srgba := src0.RGBA64At(sx, sy)
				d := dst.data[i : i+4 : i+4]
				d[0] = uint8(srgba.B >> 8)
				d[1] = uint8(srgba.G >> 8)
				d[2] = uint8(srgba.R >> 8)
				d[3] = uint8(srgba.A >> 8)
			}
			i0 += dy * stride

		}
		return

	}

	// Use the image.Image interface, part of the standard library since Go
	// 1.0.
	//
	// This is similar to FALLBACK1.0 in DrawMask, except here the concrete
	// type of dst is known to be *image.RGBA.
	for y := y0; y != y1; y, sy = y+dy, sy+dy {
		for i, sx := i0, sx0; sx != sx1; i, sx = i+di, sx+dx {
			ma := uint32(m)
			sr, sg, sb, sa := src.At(sx, sy).RGBA()
			d := dst.data[i : i+4 : i+4] // Small cap improves performance, see https://golang.org/issue/27857

			d[0] = uint8(sb * ma / m >> 8)
			d[1] = uint8(sg * ma / m >> 8)
			d[2] = uint8(sr * ma / m >> 8)
			d[3] = uint8(sa * ma / m >> 8)

		}
		i0 += dy * stride
	}
}

func drawFillSrc(dst *WayImage, r image.Rectangle, sr, sg, sb, sa uint32) {
	sr8 := uint8(sr >> 8)
	sg8 := uint8(sg >> 8)
	sb8 := uint8(sb >> 8)
	sa8 := uint8(sa >> 8)
	// The built-in copy function is faster than a straightforward for loop to fill the destination with
	// the color, but copy requires a slice source. We therefore use a for loop to fill the first row, and
	// then use the first row as the slice source for the remaining rows.
	i0 := dst.PixOffset(r.Min.X, r.Min.Y)
	i1 := i0 + r.Dx()*4
	for i := i0; i < i1; i += 4 {
		dst.data[i+0] = sr8
		dst.data[i+1] = sg8
		dst.data[i+2] = sb8
		dst.data[i+3] = sa8
	}
	firstRow := dst.data[i0:i1]
	stride := dst.size.Dx() * 4
	for y := r.Min.Y + 1; y < r.Max.Y; y++ {
		i0 += stride
		i1 += stride
		copy(dst.data[i0:i1], firstRow)
	}
}

func drawGray(dst *WayImage, r image.Rectangle, src *image.Gray, sp image.Point) {
	i0 := (r.Min.X - dst.size.Min.X) * 4
	i1 := (r.Max.X - dst.size.Min.X) * 4
	si0 := (sp.X - src.Rect.Min.X) * 1
	yMax := r.Max.Y - dst.size.Min.Y

	y := r.Min.Y - dst.size.Min.Y
	sy := sp.Y - src.Rect.Min.Y

	stride := dst.size.Dx() * 4

	for ; y != yMax; y, sy = y+1, sy+1 {
		dpix := dst.data[y*stride:]
		spix := src.Pix[sy*src.Stride:]

		for i, si := i0, si0; i < i1; i, si = i+4, si+1 {
			p := spix[si]
			d := dpix[i : i+4 : i+4] // Small cap improves performance, see https://golang.org/issue/27857
			d[0] = p
			d[1] = p
			d[2] = p
			d[3] = 255
		}
	}
}

func drawCMYK(dst *WayImage, r image.Rectangle, src *image.CMYK, sp image.Point) {
	i0 := (r.Min.X - dst.size.Min.X) * 4
	i1 := (r.Max.X - dst.size.Min.X) * 4
	si0 := (sp.X - src.Rect.Min.X) * 4
	yMax := r.Max.Y - dst.size.Min.Y

	y := r.Min.Y - dst.size.Min.Y
	sy := sp.Y - src.Rect.Min.Y
	stride := dst.size.Dx() * 4

	for ; y != yMax; y, sy = y+1, sy+1 {
		dpix := dst.data[y*stride:]
		spix := src.Pix[sy*src.Stride:]

		for i, si := i0, si0; i < i1; i, si = i+4, si+4 {
			s := spix[si : si+4 : si+4] // Small cap improves performance, see https://golang.org/issue/27857
			d := dpix[i : i+4 : i+4]
			d[2], d[1], d[0] = color.CMYKToRGB(s[0], s[1], s[2], s[3])
			d[3] = 255
		}
	}
}
