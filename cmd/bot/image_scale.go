// Scaling an image down to the size Telegram stores, so its upload carries fewer bytes.

package main

import (
	"bytes"
	"encoding/binary"
	goimage "image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"runtime"
	"sync"

	xdraw "golang.org/x/image/draw"
)

const (
	// maxImageSide is the longest side Telegram stores a photo at.
	maxImageSide = 2560
	// maxScaleBytes caps the memory decoding an image to scale it may take.
	// Scaling takes up to 26 MB more, for the scaled image.
	maxScaleBytes = 128 << 20
)

// scaleMu runs one scaling at a time, so their memory does not add up.
var scaleMu sync.Mutex

// scaleDown returns the image as a JPEG at most maxImageSide a side,
// and the size of what it returns.
// It returns the data as it is when the image fits, decoding it would take over maxScaleBytes,
// or it is a JPEG with an EXIF orientation that rotates or flips it, which scaling would drop.
func scaleDown(data []byte, cfg goimage.Config, format string) ([]byte, goimage.Point, error) {
	size := goimage.Pt(cfg.Width, cfg.Height)
	if max(size.X, size.Y) <= maxImageSide ||
		decodeBytes(data, cfg, format) > maxScaleBytes ||
		format == "jpeg" && jpegOrientation(data) != 1 {
		return data, size, nil
	}
	long, short := max(size.X, size.Y), min(size.X, size.Y)
	short = max(1, (short*maxImageSide+long/2)/long)
	size = goimage.Pt(maxImageSide, short)
	if cfg.Height > cfg.Width {
		size = goimage.Pt(short, maxImageSide)
	}
	scaleMu.Lock()
	defer scaleMu.Unlock()
	scaled, err := scale(data, size)
	// Frees the decoded image before the next scaling allocates its own
	runtime.GC()
	return scaled, size, err
}

// scale decodes the image and encodes it as a JPEG of the size, white under any transparency.
func scale(data []byte, size goimage.Point) ([]byte, error) {
	src, _, err := goimage.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	dst := goimage.NewRGBA(goimage.Rectangle{Max: size})
	draw.Draw(dst, dst.Bounds(), goimage.White, goimage.Point{}, draw.Src)
	// A kernel would take a scratch buffer of 32 bytes per destination column per source row
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// decodeBytes estimates the memory Go's decoder takes for the image, by its format and header.
func decodeBytes(data []byte, cfg goimage.Config, format string) int {
	pixels := cfg.Width * cfg.Height
	switch format {
	case "jpeg":
		samples, perPixel, progressive, ok := jpegFrame(data)
		if !ok {
			return math.MaxInt
		}
		n := pixels * samples / perPixel
		if progressive {
			// A progressive JPEG also holds a 4-byte coefficient for every sample
			n *= 5
		}
		if cfg.ColorModel == color.RGBAModel || cfg.ColorModel == color.CMYKModel {
			// An RGB or four-component JPEG is copied into an image of 4 bytes a pixel
			n += 4 * pixels
		}
		return n
	case "png":
		bytesPerPixel := 4
		if cfg.ColorModel == color.RGBA64Model || cfg.ColorModel == color.NRGBA64Model {
			bytesPerPixel = 8
		}
		// An interlaced PNG holds its passes besides the image
		return 2 * pixels * bytesPerPixel
	default:
		// A lossless WebP decodes through a second copy of its pixels
		return 8 * pixels
	}
}

// jpegFrame reads a JPEG's frame header: its samples per pixel as samples/perPixel,
// and whether it is progressive.
func jpegFrame(data []byte) (samples, perPixel int, progressive, ok bool) {
	jpegSegments(data, func(marker byte, segment []byte) bool {
		if marker < 0xc0 || marker > 0xc2 {
			return true
		}
		if len(segment) < 6 || len(segment) < 6+3*int(segment[5]) {
			return false
		}
		hMax, vMax := 1, 1
		for c := range int(segment[5]) {
			hv := segment[6+3*c+1]
			h, v := int(hv>>4), int(hv&0xf)
			samples += h * v
			hMax, vMax = max(hMax, h), max(vMax, v)
		}
		perPixel, progressive, ok = hMax*vMax, marker == 0xc2, true
		return false
	})
	return samples, perPixel, progressive, ok
}

// jpegOrientation reads a JPEG's EXIF orientation, 1 for none.
func jpegOrientation(data []byte) int {
	orientation := 1
	jpegSegments(data, func(marker byte, segment []byte) bool {
		if marker != 0xe1 {
			return true
		}
		tiff, found := bytes.CutPrefix(segment, []byte("Exif\x00\x00"))
		if !found || len(tiff) < 8 {
			return true
		}
		var order binary.ByteOrder = binary.BigEndian
		if string(tiff[:2]) == "II" {
			order = binary.LittleEndian
		}
		ifd := int(order.Uint32(tiff[4:]))
		if ifd < 0 || ifd+2 > len(tiff) {
			return false
		}
		for e := ifd + 2; e+12 <= len(tiff) && e < ifd+2+12*int(order.Uint16(tiff[ifd:])); e += 12 {
			if order.Uint16(tiff[e:]) == 0x0112 {
				orientation = int(order.Uint16(tiff[e+8:]))
			}
		}
		return false
	})
	return orientation
}

// jpegSegments calls f with each marker segment of a JPEG ahead of its scan, while f returns true.
func jpegSegments(data []byte, f func(marker byte, segment []byte) bool) {
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xff {
			return
		}
		marker := data[i+1]
		if marker == 0xff {
			i++
			continue
		}
		if marker >= 0xd0 && marker <= 0xd7 || marker == 0x01 {
			i += 2
			continue
		}
		length := int(binary.BigEndian.Uint16(data[i+2:]))
		if marker == 0xda || length < 2 || !f(marker, data[i+4:min(len(data), i+2+length)]) {
			return
		}
		i += 2 + length
	}
}
