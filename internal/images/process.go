// Package images implements the contact-photo ingest pipeline: decode, correct EXIF orientation, strip all metadata (by re-encoding), downscale to a cap, and re-encode as a single normalized JPEG.
// The CGO-free constraint means HEIC is not supported; such inputs fail to decode and are rejected by the caller.
package images

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder

	xdraw "golang.org/x/image/draw"
)

// MaxDimension is the longest-edge cap for the normalized avatar.
const MaxDimension = 512

// jpegQuality is the re-encode quality; ~80 keeps BLOBs small with little visible loss.
const jpegQuality = 80

// maxDecodedPixels bounds the pixel count we are willing to decode.
// A small, highly compressible file can declare huge dimensions and force a multi-GB RGBA allocation (memory-exhaustion DoS); the request body cap does not bound decoded pixels, so we check the header first.
// 40 MP comfortably exceeds any real avatar while blocking pathological inputs.
const maxDecodedPixels = 40_000_000

// maxDimensionInput rejects absurd single-axis dimensions before the multiply.
const maxDimensionInput = 20000

// ErrImageTooLarge is returned when an image's declared dimensions exceed the decode caps, before any full-resolution buffer is allocated.
var ErrImageTooLarge = fmt.Errorf("images: image dimensions exceed the allowed maximum")

// Process decodes an uploaded image, applies EXIF orientation, downscales it to fit MaxDimension while preserving aspect ratio, and returns a metadata-free JPEG.
// Re-encoding from decoded pixels discards all original metadata (EXIF, GPS).
// It returns an error if the bytes are not a decodable image.
func Process(data []byte) ([]byte, error) {
	// Bound decoded pixels from the header before allocating any full-resolution buffer.
	// DecodeConfig reads only the dimensions, not the pixel data.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("images: decode config (unsupported or corrupt image): %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > maxDimensionInput || cfg.Height > maxDimensionInput ||
		int64(cfg.Width)*int64(cfg.Height) > maxDecodedPixels {
		return nil, ErrImageTooLarge
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("images: decode (unsupported or corrupt image): %w", err)
	}

	img = applyOrientation(img, readOrientation(data))
	img = downscale(img, MaxDimension)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("images: encode: %w", err)
	}
	return buf.Bytes(), nil
}

// downscale returns an image fitting within max×max, preserving aspect ratio.
// It only shrinks; smaller images are returned unchanged.
func downscale(src image.Image, max int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return src
	}
	var tw, th int
	if w >= h {
		tw = max
		th = h * max / w
	} else {
		th = max
		tw = w * max / h
	}
	if tw < 1 {
		tw = 1
	}
	if th < 1 {
		th = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	return dst
}

// applyOrientation returns src rotated/flipped so it displays upright, given an EXIF orientation value (1–8).
// Orientation 1 (and anything unrecognized) is a no-op.
func applyOrientation(src image.Image, orientation int) image.Image {
	switch orientation {
	case 2:
		return flipH(src)
	case 3:
		return rotate180(src)
	case 4:
		return flipV(src)
	case 5:
		return flipH(rotate90(src))
	case 6:
		return rotate90(src)
	case 7:
		return flipH(rotate270(src))
	case 8:
		return rotate270(src)
	default:
		return src
	}
}

func flipH(src image.Image) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func flipV(src image.Image) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func rotate180(src image.Image) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// rotate90 rotates 90° clockwise.
func rotate90(src image.Image) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// rotate270 rotates 90° counter-clockwise.
func rotate270(src image.Image) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// readOrientation extracts the EXIF orientation (1–8) from JPEG bytes, returning 1 when there is no EXIF, it is unparesable, or the image is not a JPEG.
// It is deliberately defensive: any malformed structure yields 1 rather than an error.
func readOrientation(data []byte) int {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		if marker == 0xDA || marker == 0xD9 { // start of scan / end of image
			return 1
		}
		segLen := int(data[i+2])<<8 | int(data[i+3])
		if segLen < 2 || i+2+segLen > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+segLen]
		if marker == 0xE1 && len(seg) >= 6 && string(seg[:6]) == "Exif\x00\x00" {
			return orientationFromTIFF(seg[6:])
		}
		i += 2 + segLen
	}
	return 1
}

func orientationFromTIFF(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch {
	case tiff[0] == 'I' && tiff[1] == 'I':
		bo = binary.LittleEndian
	case tiff[0] == 'M' && tiff[1] == 'M':
		bo = binary.BigEndian
	default:
		return 1
	}
	ifdOffset := int(bo.Uint32(tiff[4:8]))
	if ifdOffset+2 > len(tiff) || ifdOffset < 8 {
		return 1
	}
	count := int(bo.Uint16(tiff[ifdOffset:]))
	entries := tiff[ifdOffset+2:]
	for k := 0; k < count; k++ {
		off := k * 12
		if off+12 > len(entries) {
			return 1
		}
		entry := entries[off : off+12]
		if bo.Uint16(entry[0:2]) == 0x0112 { // Orientation tag
			v := int(bo.Uint16(entry[8:10]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
