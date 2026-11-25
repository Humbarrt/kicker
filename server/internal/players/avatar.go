package players

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // decoder
	"net/http"
)

// Picture sizes: the large one for profiles, the small one for lists and the scoreboard.
// (The side displays get their own versions in M2.)
const (
	SizeLarge = 512
	SizeSmall = 128

	maxUpload = 8 << 20
	maxPixels = 4096 * 4096 // the web UI scales pictures down before uploading; this is a guard
)

// ErrBadImage is returned for uploads that aren't a usable JPEG or PNG picture.
var ErrBadImage = errors.New("not a usable JPEG or PNG picture (at least 128 × 128 pixels)")

// Avatar is a processed profile picture: cropped to a square, resized, re-encoded as JPEG (which
// also drops any metadata such as the GPS position), named by the hash of the large version.
type Avatar struct {
	Hash  string
	Large []byte
	Small []byte
}

// ProcessAvatar checks an upload by its content (not its name) and makes both sizes.
func ProcessAvatar(data []byte) (Avatar, error) {
	if len(data) == 0 || len(data) > maxUpload {
		return Avatar{}, fmt.Errorf("%w: the file must be smaller than 8 MB", ErrBadImage)
	}
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/png":
	default:
		return Avatar{}, ErrBadImage
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < SizeSmall || cfg.Height < SizeSmall || cfg.Width*cfg.Height > maxPixels {
		return Avatar{}, ErrBadImage
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Avatar{}, ErrBadImage
	}
	sq := cropSquare(img)
	side := sq.Bounds().Dx()
	large := resize(sq, min(side, SizeLarge))
	small := resize(large, SizeSmall)

	var a Avatar
	if a.Large, err = encode(large); err != nil {
		return Avatar{}, err
	}
	if a.Small, err = encode(small); err != nil {
		return Avatar{}, err
	}
	sum := sha256.Sum256(a.Large)
	a.Hash = hex.EncodeToString(sum[:])
	return a, nil
}

func encode(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 85})
	return b.Bytes(), err
}

// cropSquare takes the centred square of the picture (without copying).
func cropSquare(img image.Image) image.Image {
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	x0 := b.Min.X + (b.Dx()-side)/2
	y0 := b.Min.Y + (b.Dy()-side)/2
	r := image.Rect(x0, y0, x0+side, y0+side)
	if s, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return s.SubImage(r)
	}
	out := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			out.Set(x, y, img.At(x0+x, y0+y))
		}
	}
	return out
}

type px = [4]float32

// resize scales a square picture down to n × n by area averaging (every source pixel contributes
// with the share of its area that falls into the target pixel), separably: each source row is
// read once and shrunk to n columns, then the columns are shrunk to n rows.
func resize(img image.Image, n int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	row := make([]px, w)
	tmp := make([]px, n*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			row[x] = px{float32(r), float32(g), float32(bl), float32(a)}
		}
		area1D(row, 1, tmp[y*n:], 1, w, n)
	}
	dst := make([]px, n*n)
	for x := 0; x < n; x++ {
		area1D(tmp[x:], n, dst[x:], n, h, n)
	}
	out := image.NewRGBA(image.Rect(0, 0, n, n))
	for i, p := range dst {
		out.Set(i%n, i/n, color.RGBA64{uint16(p[0] + .5), uint16(p[1] + .5), uint16(p[2] + .5), uint16(p[3] + .5)})
	}
	return out
}

// area1D resamples srcN values (stride ss) into dstN values (stride ds).
func area1D(src []px, ss int, dst []px, ds int, srcN, dstN int) {
	scale := float64(srcN) / float64(dstN)
	for i := 0; i < dstN; i++ {
		lo, hi := float64(i)*scale, float64(i+1)*scale
		var acc [4]float64
		var weight float64
		for j := int(lo); j < srcN && float64(j) < hi; j++ {
			wgt := min(float64(j+1), hi) - max(float64(j), lo)
			if wgt <= 0 {
				continue
			}
			p := src[j*ss]
			for k := 0; k < 4; k++ {
				acc[k] += float64(p[k]) * wgt
			}
			weight += wgt
		}
		for k := 0; k < 4; k++ {
			dst[i*ds][k] = float32(acc[k] / weight)
		}
	}
}
