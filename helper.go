package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"os"
	"reflect"
	"runtime"
)

func bufNeed(buf []byte, off int, n int) {
	if off+n > len(buf) {
		_, _ = fmt.Fprintf(os.Stderr, "bufNeed: off=%d n=%d len=%d\n", off, n, len(buf))
		panic("wayland: buffer underrun")
	}
}

func bufReadU32(buf []byte, off *int) uint32 {
	bufNeed(buf, *off, 4)
	x := binary.LittleEndian.Uint32(buf[*off:])
	*off += 4
	return x
}

func bufReadU16(buf []byte, off *int) uint16 {
	bufNeed(buf, *off, 2)
	x := binary.LittleEndian.Uint16(buf[*off:])
	*off += 2
	return x
}

func bufReadN(buf []byte, off *int, n int) []byte {
	bufNeed(buf, *off, n)
	result := make([]byte, n)
	copy(result, buf[*off:*off+n])
	*off += n
	return result
}

func roundup4(n uint32) uint32 {
	return (n + 3) &^ uint32(3)
}

func bufReadString(buf []byte) string {
	last := len(buf)
	for buf[last-1] == 0 {
		last--
	}
	return string(buf[:last])
}

func xdgTopLevelStateToName(state uint32) string {
	switch state {
	case 1:
		return "maximized"
	case 2:
		return "fullscreen"
	case 3:
		return "resizing"
	case 4:
		return "activated"
	case 5:
		return "tiled_left"
	case 6:
		return "tiled_right"
	case 7:
		return "tiled_top"
	case 8:
		return "tiled_bottom"
	case 9:
		return "suspended"
	case 10:
		return "contrained_left"
	case 11:
		return "contrained_right"
	case 12:
		return "contrained_top"
	case 13:
		return "contrained_bottom"
	}
	return "unknown"
}

func xdgTopLevelCapabilitiesToName(capability uint32) string {
	switch capability {
	case 1:
		return "window_menu"
	case 2:
		return "maximize"
	case 3:
		return "fullscreen"
	case 4:
		return "minimize"
	}
	return "unknown"
}

type Fixed int32

// ToFloat converts a Wayland fixed-point value to float64
func (f Fixed) ToFloat() float64 {
	return float64(f) / 256.0
}

// FixedFromFloat converts a float64 to a Wayland fixed-point value
func FixedFromFloat(v float64) Fixed {
	return Fixed(math.Round(v * 256.0))
}

func colorFormatToName(format uint32) string {

	switch format {
	case 0:
		return "argb8888" //  "32-bit ARGB format, [31:0] A:R:G:B 8:8:8:8 little endian"
	case 1:
		return "xrgb8888" //  "32-bit RGB format, [31:0] x:R:G:B 8:8:8:8 little endian"
	case 0x20203843:
		return "c8" //  "8-bit color index format, [7:0] C"
	case 0x38424752:
		return "rgb332" //  "8-bit RGB format, [7:0] R:G:B 3:3:2"
	case 0x38524742:
		return "bgr233" //  "8-bit BGR format, [7:0] B:G:R 2:3:3"
	case 0x32315258:
		return "xrgb4444" //  "16-bit xRGB format, [15:0] x:R:G:B 4:4:4:4 little endian"
	case 0x32314258:
		return "xbgr4444" //  "16-bit xBGR format, [15:0] x:B:G:R 4:4:4:4 little endian"
	case 0x32315852:
		return "rgbx4444" //  "16-bit RGBx format, [15:0] R:G:B:x 4:4:4:4 little endian"
	case 0x32315842:
		return "bgrx4444" //  "16-bit BGRx format, [15:0] B:G:R:x 4:4:4:4 little endian"
	case 0x32315241:
		return "argb4444" //  "16-bit ARGB format, [15:0] A:R:G:B 4:4:4:4 little endian"
	case 0x32314241:
		return "abgr4444" //  "16-bit ABGR format, [15:0] A:B:G:R 4:4:4:4 little endian"
	case 0x32314152:
		return "rgba4444" //  "16-bit RGBA format, [15:0] R:G:B:A 4:4:4:4 little endian"
	case 0x32314142:
		return "bgra4444" //  "16-bit BGRA format, [15:0] B:G:R:A 4:4:4:4 little endian"
	case 0x35315258:
		return "xrgb1555" //  "16-bit xRGB format, [15:0] x:R:G:B 1:5:5:5 little endian"
	case 0x35314258:
		return "xbgr1555" //  "16-bit xBGR 1555 format, [15:0] x:B:G:R 1:5:5:5 little endian"
	case 0x35315852:
		return "rgbx5551" //  "16-bit RGBx 5551 format, [15:0] R:G:B:x 5:5:5:1 little endian"
	case 0x35315842:
		return "bgrx5551" //  "16-bit BGRx 5551 format, [15:0] B:G:R:x 5:5:5:1 little endian"
	case 0x35315241:
		return "argb1555" //  "16-bit ARGB 1555 format, [15:0] A:R:G:B 1:5:5:5 little endian"
	case 0x35314241:
		return "abgr1555" //  "16-bit ABGR 1555 format, [15:0] A:B:G:R 1:5:5:5 little endian"
	case 0x35314152:
		return "rgba5551" //  "16-bit RGBA 5551 format, [15:0] R:G:B:A 5:5:5:1 little endian"
	case 0x35314142:
		return "bgra5551" //  "16-bit BGRA 5551 format, [15:0] B:G:R:A 5:5:5:1 little endian"
	case 0x36314752:
		return "rgb565" //  "16-bit RGB 565 format, [15:0] R:G:B 5:6:5 little endian"
	case 0x36314742:
		return "bgr565" //  "16-bit BGR 565 format, [15:0] B:G:R 5:6:5 little endian"
	case 0x34324752:
		return "rgb888" //  "24-bit RGB format, [23:0] R:G:B little endian"
	case 0x34324742:
		return "bgr888" //  "24-bit BGR format, [23:0] B:G:R little endian"
	case 0x34324258:
		return "xbgr8888" //  "32-bit xBGR format, [31:0] x:B:G:R 8:8:8:8 little endian"
	case 0x34325852:
		return "rgbx8888" //  "32-bit RGBx format, [31:0] R:G:B:x 8:8:8:8 little endian"
	case 0x34325842:
		return "bgrx8888" //  "32-bit BGRx format, [31:0] B:G:R:x 8:8:8:8 little endian"
	case 0x34324241:
		return "abgr8888" //  "32-bit ABGR format, [31:0] A:B:G:R 8:8:8:8 little endian"
	case 0x34324152:
		return "rgba8888" //  "32-bit RGBA format, [31:0] R:G:B:A 8:8:8:8 little endian"
	case 0x34324142:
		return "bgra8888" //  "32-bit BGRA format, [31:0] B:G:R:A 8:8:8:8 little endian"
	case 0x30335258:
		return "xrgb2101010" //  "32-bit xRGB format, [31:0] x:R:G:B 2:10:10:10 little endian"
	case 0x30334258:
		return "xbgr2101010" //  "32-bit xBGR format, [31:0] x:B:G:R 2:10:10:10 little endian"
	case 0x30335852:
		return "rgbx1010102" //  "32-bit RGBx format, [31:0] R:G:B:x 10:10:10:2 little endian"
	case 0x30335842:
		return "bgrx1010102" //  "32-bit BGRx format, [31:0] B:G:R:x 10:10:10:2 little endian"
	case 0x30335241:
		return "argb2101010" //  "32-bit ARGB format, [31:0] A:R:G:B 2:10:10:10 little endian"
	case 0x30334241:
		return "abgr2101010" //  "32-bit ABGR format, [31:0] A:B:G:R 2:10:10:10 little endian"
	case 0x30334152:
		return "rgba1010102" //  "32-bit RGBA format, [31:0] R:G:B:A 10:10:10:2 little endian"
	case 0x30334142:
		return "bgra1010102" //  "32-bit BGRA format, [31:0] B:G:R:A 10:10:10:2 little endian"
	case 0x56595559:
		return "yuyv" //  "packed YCbCr format, [31:0] Cr0:Y1:Cb0:Y0 8:8:8:8 little endian"
	case 0x55595659:
		return "yvyu" //  "packed YCbCr format, [31:0] Cb0:Y1:Cr0:Y0 8:8:8:8 little endian"
	case 0x59565955:
		return "uyvy" //  "packed YCbCr format, [31:0] Y1:Cr0:Y0:Cb0 8:8:8:8 little endian"
	case 0x59555956:
		return "vyuy" //  "packed YCbCr format, [31:0] Y1:Cb0:Y0:Cr0 8:8:8:8 little endian"
	case 0x56555941:
		return "ayuv" //  "packed AYCbCr format, [31:0] A:Y:Cb:Cr 8:8:8:8 little endian"
	case 0x3231564e:
		return "nv12" //  "2 plane YCbCr Cr:Cb format, 2x2 subsampled Cr:Cb plane"
	case 0x3132564e:
		return "nv21" //  "2 plane YCbCr Cb:Cr format, 2x2 subsampled Cb:Cr plane"
	case 0x3631564e:
		return "nv16" //  "2 plane YCbCr Cr:Cb format, 2x1 subsampled Cr:Cb plane"
	case 0x3136564e:
		return "nv61" //  "2 plane YCbCr Cb:Cr format, 2x1 subsampled Cb:Cr plane"
	case 0x39565559:
		return "yuv410" //  "3 plane YCbCr format, 4x4 subsampled Cb (1) and Cr (2) planes"
	case 0x39555659:
		return "yvu410" //  "3 plane YCbCr format, 4x4 subsampled Cr (1) and Cb (2) planes"
	case 0x31315559:
		return "yuv411" //  "3 plane YCbCr format, 4x1 subsampled Cb (1) and Cr (2) planes"
	case 0x31315659:
		return "yvu411" //  "3 plane YCbCr format, 4x1 subsampled Cr (1) and Cb (2) planes"
	case 0x32315559:
		return "yuv420" //  "3 plane YCbCr format, 2x2 subsampled Cb (1) and Cr (2) planes"
	case 0x32315659:
		return "yvu420" //  "3 plane YCbCr format, 2x2 subsampled Cr (1) and Cb (2) planes"
	case 0x36315559:
		return "yuv422" //  "3 plane YCbCr format, 2x1 subsampled Cb (1) and Cr (2) planes"
	case 0x36315659:
		return "yvu422" //  "3 plane YCbCr format, 2x1 subsampled Cr (1) and Cb (2) planes"
	case 0x34325559:
		return "yuv444" //  "3 plane YCbCr format, non-subsampled Cb (1) and Cr (2) planes"
	case 0x34325659:
		return "yvu444" //  "3 plane YCbCr format, non-subsampled Cr (1) and Cb (2) planes"
	case 0x20203852:
		return "r8" // "[7:0] R"
	case 0x20363152:
		return "r16" //  "[15:0] R little endian"
	case 0x38384752:
		return "rg88" //  "[15:0] R:G 8:8 little endian"
	case 0x38385247:
		return "gr88" //  "[15:0] G:R 8:8 little endian"
	case 0x32334752:
		return "rg1616" //  "[31:0] R:G 16:16 little endian"
	case 0x32335247:
		return "gr1616" //  "[31:0] G:R 16:16 little endian"
	case 0x48345258:
		return "xrgb16161616f" //  "[63:0] x:R:G:B 16:16:16:16 little endian"
	case 0x48344258:
		return "xbgr16161616f" //  "[63:0] x:B:G:R 16:16:16:16 little endian"
	case 0x48345241:
		return "argb16161616f" //  "[63:0] A:R:G:B 16:16:16:16 little endian"
	case 0x48344241:
		return "abgr16161616f" //  "[63:0] A:B:G:R 16:16:16:16 little endian"
	case 0x56555958:
		return "xyuv8888" //  "[31:0] X:Y:Cb:Cr 8:8:8:8 little endian"
	case 0x34325556:
		return "vuy888" //  "[23:0] Cr:Cb:Y 8:8:8 little endian"
	case 0x30335556:
		return "vuy101010" //  "Y followed by U then V, 10:10:10. Non-linear modifier only"
	case 0x30313259:
		return "y210" //  "[63:0] Cr0:0:Y1:0:Cb0:0:Y0:0 10:6:10:6:10:6:10:6 little endian per 2 Y pixels"
	case 0x32313259:
		return "y212" //  "[63:0] Cr0:0:Y1:0:Cb0:0:Y0:0 12:4:12:4:12:4:12:4 little endian per 2 Y pixels"
	case 0x36313259:
		return "y216" //  "[63:0] Cr0:Y1:Cb0:Y0 16:16:16:16 little endian per 2 Y pixels"
	case 0x30313459:
		return "y410" //  "[31:0] A:Cr:Y:Cb 2:10:10:10 little endian"
	case 0x32313459:
		return "y412" //  "[63:0] A:0:Cr:0:Y:0:Cb:0 12:4:12:4:12:4:12:4 little endian"
	case 0x36313459:
		return "y416" //  "[63:0] A:Cr:Y:Cb 16:16:16:16 little endian"
	case 0x30335658:
		return "xvyu2101010" //  "[31:0] X:Cr:Y:Cb 2:10:10:10 little endian"
	case 0x36335658:
		return "xvyu12_16161616" //  "[63:0] X:0:Cr:0:Y:0:Cb:0 12:4:12:4:12:4:12:4 little endian"
	case 0x38345658:
		return "xvyu16161616" //  "[63:0] X:Cr:Y:Cb 16:16:16:16 little endian"
	case 0x304c3059:
		return "y0l0" //  "[63:0]   A3:A2:Y3:0:Cr0:0:Y2:0:A1:A0:Y1:0:Cb0:0:Y0:0  1:1:8:2:8:2:8:2:1:1:8:2:8:2:8:2 little endian"
	case 0x304c3058:
		return "x0l0" //  "[63:0]   X3:X2:Y3:0:Cr0:0:Y2:0:X1:X0:Y1:0:Cb0:0:Y0:0  1:1:8:2:8:2:8:2:1:1:8:2:8:2:8:2 little endian"
	case 0x324c3059:
		return "y0l2" //  "[63:0]   A3:A2:Y3:Cr0:Y2:A1:A0:Y1:Cb0:Y0  1:1:10:10:10:1:1:10:10:10 little endian"
	case 0x324c3058:
		return "x0l2" //  "[63:0]   X3:X2:Y3:Cr0:Y2:X1:X0:Y1:Cb0:Y0  1:1:10:10:10:1:1:10:10:10 little endian"
	case 0x38305559:
		return "yuv420_8bit" //  0x30315559 0x38415258 0x38414258 0x38415852 0x38415842 0x38413852 0x38413842 0x38413552 0x38413542 0x3432564e "non-subsampled Cr:Cb plane"
	case 0x3234564e:
		return "nv42" //  "non-subsampled Cb:Cr plane"
	case 0x30313250:
		return "p210" //  "2x1 subsampled Cr:Cb plane, 10 bit per channel"
	case 0x30313050:
		return "p010" //  "2x2 subsampled Cr:Cb plane 10 bits per channel"
	case 0x32313050:
		return "p012" //  "2x2 subsampled Cr:Cb plane 12 bits per channel"
	case 0x36313050:
		return "p016" //  "2x2 subsampled Cr:Cb plane 16 bits per channel"
	case 0x30314241:
		return "axbxgxrx106106106106" //  "[63:0] A:x:B:x:G:x:R:x 10:6:10:6:10:6:10:6 little endian"
	case 0x3531564e:
		return "nv15" // "2x2 subsampled Cr:Cb plane"
	case 0x30313451:
		return "q410" //  0x31303451 0x38345258 "[63:0] x:R:G:B 16:16:16:16 little endian"
	case 0x38344258:
		return "xbgr16161616" //  "[63:0] x:B:G:R 16:16:16:16 little endian"
	case 0x38345241:
		return "argb16161616" //  "[63:0] A:R:G:B 16:16:16:16 little endian"
	case 0x38344241:
		return "abgr16161616" //  "[63:0] A:B:G:R 16:16:16:16 little endian"
	case 0x20203143:
		return "c1" //  "[7:0] C0:C1:C2:C3:C4:C5:C6:C7 1:1:1:1:1:1:1:1 eight pixels/byte"
	case 0x20203243:
		return "c2" //  "[7:0] C0:C1:C2:C3 2:2:2:2 four pixels/byte"
	case 0x20203443:
		return "c4" //  "[7:0] C0:C1 4:4 two pixels/byte"
	case 0x20203144:
		return "d1" //  "[7:0] D0:D1:D2:D3:D4:D5:D6:D7 1:1:1:1:1:1:1:1 eight pixels/byte"
	case 0x20203244:
		return "d2" //  "[7:0] D0:D1:D2:D3 2:2:2:2 four pixels/byte"
	case 0x20203444:
		return "d4" //  "[7:0] D0:D1 4:4 two pixels/byte"
	case 0x20203844:
		return "d8" // "[7:0] D"
	case 0x20203152:
		return "r1" //  "[7:0] R0:R1:R2:R3:R4:R5:R6:R7 1:1:1:1:1:1:1:1 eight pixels/byte"
	case 0x20203252:
		return "r2" //  "[7:0] R0:R1:R2:R3 2:2:2:2 four pixels/byte"
	case 0x20203452:
		return "r4" //  "[7:0] R0:R1 4:4 two pixels/byte"
	case 0x20303152:
		return "r10" //  "[15:0] x:R 6:10 little endian"
	case 0x20323152:
		return "r12" //  "[15:0] x:R 4:12 little endian"
	case 0x59555641:
		return "avuy8888" //  "[31:0] A:Cr:Cb:Y 8:8:8:8 little endian"
	case 0x59555658:
		return "xvuy8888" //  "[31:0] X:Cr:Cb:Y 8:8:8:8 little endian"
	case 0x30333050:
		return "p030" //  "2x2 subsampled Cr:Cb plane 10 bits per channel packed"
	case 0x38344752:
		return "rgb161616" //  "[47:0] R:G:B 16:16:16 little endian"
	case 0x38344742:
		return "bgr161616" //  "[47:0] B:G:R 16:16:16 little endian"
	case 0x48202052:
		return "r16f" //  "[15:0] R 16 little endian"
	case 0x48205247:
		return "gr1616f" //  "[31:0] G:R 16:16 little endian"
	case 0x48524742:
		return "bgr161616f" //  "[47:0] B:G:R 16:16:16 little endian"
	case 0x46202052:
		return "r32f" //  "[31:0] R 32 little endian"
	case 0x46205247:
		return "gr3232f" //  "[63:0] G:R 32:32 little endian"
	case 0x46524742:
		return "bgr323232f" //  "[95:0] B:G:R 32:32:32 little endian"
	case 0x46384241:
		return "abgr32323232f" //  "[127:0] A:B:G:R 32:32:32:32 little endian"
	case 0x3032564e:
		return "nv20" // "2x1 subsampled Cr:Cb plane"
	case 0x3033564e:
		return "nv30" //  "non-subsampled Cr:Cb plane"
	case 0x30313053:
		return "s010" //  "2x2 subsampled Cb (1) and Cr (2) planes 10 bits per channel"
	case 0x30313253:
		return "s210" //  "2x1 subsampled Cb (1) and Cr (2) planes 10 bits per channel"
	case 0x30313453:
		return "s410" //  "non-subsampled Cb (1) and Cr (2) planes 10 bits per channel"
	case 0x32313053:
		return "s012" //  "2x2 subsampled Cb (1) and Cr (2) planes 12 bits per channel"
	case 0x32313253:
		return "s212" //  "2x1 subsampled Cb (1) and Cr (2) planes 12 bits per channel"
	case 0x32313453:
		return "s412" //  "non-subsampled Cb (1) and Cr (2) planes 12 bits per channel"
	case 0x36313053:
		return "s016" //  "2x2 subsampled Cb (1) and Cr (2) planes 16 bits per channel"
	case 0x36313253:
		return "s216" //  "2x1 subsampled Cb (1) and Cr (2) planes 16 bits per channel"
	case 0x36313453:
		return "s416" //  "non-subsampled Cb (1) and Cr (2) planes 16 bits per channel"
	case 0x30335958:
		return "xvuy2101010" //  "[31:0] x:Cr:Cb:Y 2:10:10:10 little endian"
	case 0x30333250:
		return "p230" //  "2x1 subsampled Cr:Cb plane 10 bits per channel packed"
	case 0x30333454:
		return "t430" //  0x59455247 "8-bit Y-only"
	case 0x34415059:
		return "xyyy2101010" //  "[31:0] x:Y2:Y1:Y0 2:10:10:10 little endian"

	}
	return "unknown format"
}

func LogFailedClose(f func() error) {
	err := f()
	if err == nil {
		return
	}

	type_ := reflect.TypeOf(f)
	name := type_.Name()
	if type_.Kind() == reflect.Func {
		name = runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
	}

	pc, file, line, ok := runtime.Caller(1)
	if !ok {
		log.Printf("failed to get caller info for failed close: %v\n", err)
		return
	}

	Func := runtime.FuncForPC(pc)

	fmt.Printf("failed to close %s in %s(%s:%d) with %v\n", name, Func.Name(), file, line, err)

}
