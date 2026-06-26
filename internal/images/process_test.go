package images

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return img
}

func TestProcessDownscalesPreservingAspectAndOutputsJPEG(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1000, 500))
	out, err := Process(encodePNG(t, src))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(out)); err != nil || format != "jpeg" {
		t.Fatalf("output format = %q err %v, want jpeg", format, err)
	}
	got := decode(t, out).Bounds()
	if got.Dx() != 512 || got.Dy() != 256 {
		t.Errorf("downscaled to %dx%d, want 512x256", got.Dx(), got.Dy())
	}
}

func TestProcessLeavesSmallImagesUnscaled(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 80))
	out, err := Process(encodePNG(t, src))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	got := decode(t, out).Bounds()
	if got.Dx() != 100 || got.Dy() != 80 {
		t.Errorf("small image resized to %dx%d, want 100x80", got.Dx(), got.Dy())
	}
}

func TestProcessRejectsNonImage(t *testing.T) {
	if _, err := Process([]byte("this is not an image")); err == nil {
		t.Error("expected error for non-image input")
	}
}

func TestProcessStripsMetadata(t *testing.T) {
	// A JPEG carrying an EXIF APP1 segment; after processing the output must not contain it (re-encode strips metadata).
	src := image.NewRGBA(image.Rect(0, 0, 50, 50))
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, src, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	out, err := Process(raw.Bytes())
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if bytes.Contains(out, []byte("Exif\x00\x00")) {
		t.Error("output still contains an EXIF marker")
	}
}

func TestRotate90Clockwise(t *testing.T) {
	// 2x1 image: pixel (0,0) red (left), (1,0) blue (right). Rotating the strip 90° clockwise stands it upright with red on top and blue on the bottom.
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	red := color.RGBA{255, 0, 0, 255}
	blue := color.RGBA{0, 0, 255, 255}
	src.Set(0, 0, red)
	src.Set(1, 0, blue)

	dst := rotate90(src)
	if dst.Bounds().Dx() != 1 || dst.Bounds().Dy() != 2 {
		t.Fatalf("rotated dims = %v, want 1x2", dst.Bounds())
	}
	if dst.RGBAAt(0, 0) != red {
		t.Errorf("top pixel = %v, want red", dst.RGBAAt(0, 0))
	}
	if dst.RGBAAt(0, 1) != blue {
		t.Errorf("bottom pixel = %v, want blue", dst.RGBAAt(0, 1))
	}
}

func TestReadOrientation(t *testing.T) {
	// Non-JPEG / no EXIF → 1.
	if got := readOrientation([]byte{0x89, 'P', 'N', 'G'}); got != 1 {
		t.Errorf("PNG orientation = %d, want 1", got)
	}
	if got := readOrientation(nil); got != 1 {
		t.Errorf("nil orientation = %d, want 1", got)
	}

	// Hand-built JPEG with an EXIF APP1 declaring orientation = 6 (big-endian TIFF).
	tiff := []byte{'M', 'M', 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08} // header, IFD0 at offset 8
	tiff = append(tiff, 0x00, 0x01)                              // 1 entry
	tiff = append(tiff,
		0x01, 0x12, // tag Orientation
		0x00, 0x03, // type SHORT
		0x00, 0x00, 0x00, 0x01, // count 1
		0x00, 0x06, 0x00, 0x00, // value 6
	)
	exif := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xFF, 0xE1}
	segLen := len(exif) + 2
	app1 = append(app1, byte(segLen>>8), byte(segLen))
	app1 = append(app1, exif...)
	jpegBytes := append([]byte{0xFF, 0xD8}, app1...)
	jpegBytes = append(jpegBytes, 0xFF, 0xD9)

	if got := readOrientation(jpegBytes); got != 6 {
		t.Errorf("EXIF orientation = %d, want 6", got)
	}
}

// craftPNGHeader builds a minimal, CRC-valid PNG (signature + IHDR only) that declares the given dimensions, so DecodeConfig can read the size without any pixel data — used to exercise the decode dimension cap.
func craftPNGHeader(t *testing.T, w, h uint32) []byte {
	t.Helper()
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // color type: truecolor RGB
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(ihdr)))
	b.Write(length)
	typeAndData := append([]byte("IHDR"), ihdr...)
	b.Write(typeAndData)
	crc := make([]byte, 4)
	binary.BigEndian.PutUint32(crc, crc32.ChecksumIEEE(typeAndData))
	b.Write(crc)
	return b.Bytes()
}

func TestProcessRejectsOversizedDimensions(t *testing.T) {
	// 30000 > maxDimensionInput, so it must be rejected from the header alone, before any full-resolution buffer is allocated.
	if _, err := Process(craftPNGHeader(t, 30000, 30000)); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("Process(oversized) = %v, want ErrImageTooLarge", err)
	}
	// A modest header passes the cap (then fails later for lack of pixel data, which is a different, non-ErrImageTooLarge error).
	if _, err := Process(craftPNGHeader(t, 100, 100)); errors.Is(err, ErrImageTooLarge) {
		t.Fatal("Process(100x100 header) wrongly rejected as too large")
	}
}
