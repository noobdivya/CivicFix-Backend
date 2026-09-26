// Package media validates and stores uploaded photos.
package media

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register WebP decoder
)

const (
	maxDimension = 2048       // longest side after resizing
	maxPixels    = 50_000_000 // reject decompression bombs before decoding
	jpegQuality  = 85
)

var ErrNotImage = errors.New("file is not a JPEG, PNG or WebP image")

type Saved struct {
	RelPath     string // path relative to the upload dir, with forward slashes
	ContentType string
	Size        int
	Width       int
	Height      int
}

// SaveImage decodes an uploaded image, scales it down if needed and writes
// it as a fresh JPEG under uploadDir/<subdir>/YYYY/MM/. Re-encoding strips
// EXIF metadata (device info, embedded GPS) and anything that isn't pixels.
func SaveImage(r io.Reader, uploadDir, subdir string) (*Saved, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, ErrNotImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("image is too large (%dx%d)", cfg.Width, cfg.Height)
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrNotImage
	}
	img = fit(img, maxDimension)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}

	name, err := randomName()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	rel := path.Join(subdir, now.Format("2006"), now.Format("01"), name+".jpg")
	abs := filepath.Join(uploadDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(abs, out.Bytes(), 0o644); err != nil {
		return nil, err
	}

	b := img.Bounds()
	return &Saved{RelPath: rel, ContentType: "image/jpeg", Size: out.Len(), Width: b.Dx(), Height: b.Dy()}, nil
}

// Remove deletes a previously saved file (used to clean up on errors).
func Remove(uploadDir, relPath string) {
	_ = os.Remove(filepath.Join(uploadDir, filepath.FromSlash(relPath)))
}

// fit scales img down so its longest side is at most max pixels.
func fit(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return img
	}
	if w >= h {
		h = h * max / w
		w = max
	} else {
		w = w * max / h
		h = max
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

func randomName() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
