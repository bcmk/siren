package main

import (
	"bytes"
	goimage "image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

// An image over maxImageSide goes as a JPEG scaled down to it, white under transparency.
// One that fits, or would take over maxScaleBytes to decode, goes as it is.
func TestScaleDown(t *testing.T) {
	t.Parallel()
	encoded := func(img goimage.Image) []byte {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	tests := []struct {
		name string
		data []byte
		// cfg is the header's, read from data when zero.
		cfg goimage.Config
		// wantSize is the scaled JPEG's, zero for the data as it is.
		wantSize  goimage.Point
		wantWhite bool
	}{
		{"one that fits", encoded(goimage.NewGray(goimage.Rect(0, 0, 2560, 100))), goimage.Config{},
			goimage.Point{}, false},
		{"a wide one", encoded(goimage.NewGray(goimage.Rect(0, 0, 3000, 200))), goimage.Config{},
			goimage.Pt(2560, 171), false},
		{"a tall transparent one", encoded(goimage.NewNRGBA(goimage.Rect(0, 0, 150, 3000))), goimage.Config{},
			goimage.Pt(128, 2560), true},
		{"one too big to decode", jpegStart(1, 0xc2, 0x11, 0x11, 0x11), goimage.Config{Width: 5000, Height: 5000},
			goimage.Point{}, false},
		{"a JPEG with an EXIF orientation that rotates or flips it", jpegStart(6, 0xc0, 0x22, 0x11, 0x11),
			goimage.Config{Width: 3000, Height: 2000}, goimage.Point{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, format := tc.cfg, "jpeg"
			if cfg == (goimage.Config{}) {
				var err error
				if cfg, format, err = goimage.DecodeConfig(bytes.NewReader(tc.data)); err != nil {
					t.Fatal(err)
				}
			}
			got, size, err := scaleDown(tc.data, cfg, format)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantSize == (goimage.Point{}) {
				if !bytes.Equal(got, tc.data) || size != goimage.Pt(cfg.Width, cfg.Height) {
					t.Errorf("got %d bytes of %v, want the data as it is", len(got), size)
				}
				return
			}
			if size != tc.wantSize {
				t.Errorf("size = %v, want %v", size, tc.wantSize)
			}
			img, gotFormat, err := goimage.Decode(bytes.NewReader(got))
			if err != nil {
				t.Fatal(err)
			}
			if gotFormat != "jpeg" || img.Bounds().Size() != tc.wantSize {
				t.Errorf("got a %s %v, want a jpeg %v", gotFormat, img.Bounds().Size(), tc.wantSize)
			}
			if r, g, b, _ := img.At(0, 0).RGBA(); tc.wantWhite && (r < 0xf000 || g < 0xf000 || b < 0xf000) {
				t.Errorf("the top left pixel = %v, want white", img.At(0, 0))
			}
		})
	}
}

// jpegStart is a JPEG's start up to a frame header of the marker,
// after an EXIF segment of the orientation, with a component of each sampling factor.
func jpegStart(orientation, marker byte, sampling ...byte) []byte {
	b := []byte{0xff, 0xd8}
	// Exif, a big-endian TIFF header, then one IFD entry: orientation, a short
	exif := []byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00")
	exif = append(exif, orientation, 0, 0)
	b = append(b, 0xff, 0xe1, 0, byte(2+len(exif)))
	b = append(b, exif...)
	length := 8 + 3*len(sampling)
	b = append(b, 0xff, marker, byte(length>>8), byte(length), 8, 0x03, 0xe8, 0x03, 0xe8, byte(len(sampling)))
	for i, hv := range sampling {
		b = append(b, byte(i+1), hv, 0)
	}
	return b
}

// The estimate of a decode's memory follows Go's decoders.
func TestDecodeBytes(t *testing.T) {
	t.Parallel()
	const pixels = 1000 * 1000
	tests := []struct {
		name   string
		data   []byte
		format string
		model  color.Model
		want   int
	}{
		{"a baseline 4:2:0 JPEG", jpegStart(1, 0xc0, 0x22, 0x11, 0x11), "jpeg", nil, pixels * 3 / 2},
		{"a progressive 4:2:0 JPEG", jpegStart(1, 0xc2, 0x22, 0x11, 0x11), "jpeg", nil, pixels * 15 / 2},
		{"a progressive 4:4:4 JPEG", jpegStart(1, 0xc2, 0x11, 0x11, 0x11), "jpeg", nil, pixels * 15},
		{"a grey JPEG", jpegStart(1, 0xc0, 0x11), "jpeg", nil, pixels},
		{"an RGB JPEG", jpegStart(1, 0xc0, 0x11, 0x11, 0x11), "jpeg", color.RGBAModel, pixels * 7},
		{"a CMYK JPEG", jpegStart(1, 0xc0, 0x11, 0x11, 0x11, 0x11), "jpeg", color.CMYKModel, pixels * 8},
		{"a subsampled YCbCrK JPEG", jpegStart(1, 0xc0, 0x22, 0x11, 0x11, 0x22), "jpeg", color.CMYKModel,
			pixels * 13 / 2},
		{"a JPEG with no frame header", []byte{0xff, 0xd8, 0xff, 0xda, 0x00, 0x02}, "jpeg", nil, math.MaxInt},
		{"an 8-bit PNG", nil, "png", color.NRGBAModel, pixels * 8},
		{"a 16-bit PNG", nil, "png", color.NRGBA64Model, pixels * 16},
		{"a WebP", nil, "webp", nil, pixels * 8},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := goimage.Config{ColorModel: tc.model, Width: 1000, Height: 1000}
			if got := decodeBytes(tc.data, cfg, tc.format); got != tc.want {
				t.Errorf("decodeBytes = %d, want %d", got, tc.want)
			}
		})
	}
}
