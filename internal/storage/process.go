package storage

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"

	"github.com/KarpelesLab/gowebp"
	xdraw "golang.org/x/image/draw"
)

// processImage validates an uploaded image, resizes it when necessary,
// and returns its WebP-encoded bytes. Storage backends can reuse this.
func processImage(
	ctx context.Context,
	file multipart.File,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(io.LimitReader(file, MaxImageSize+1))
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	if int64(len(data)) > MaxImageSize {
		return nil, ErrImageTooLarge
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid image content: %w", err)
	}
	if format != "jpeg" && format != "png" {
		return nil, fmt.Errorf("unsupported image format: %s", format)
	}
	if config.Width <= 0 || config.Height <= 0 ||
		int64(config.Width) > MaxImagePixels/int64(config.Height) {
		return nil, ErrImageDimensions
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	if width > MaxImageWidth || height > MaxImageHeight {
		scale := min(
			float64(MaxImageWidth)/float64(width),
			float64(MaxImageHeight)/float64(height),
		)

		newWidth := max(1, int(float64(width)*scale))
		newHeight := max(1, int(float64(height)*scale))

		resized := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
		xdraw.CatmullRom.Scale(
			resized,
			resized.Bounds(),
			img,
			bounds,
			xdraw.Over,
			nil,
		)
		img = resized
	}

	var encoded bytes.Buffer
	if err := gowebp.Encode(&encoded, img, &gowebp.Options{
		Lossy:   true,
		Quality: 88,
		Method:  5,
	}); err != nil {
		return nil, fmt.Errorf("encode WebP image: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return encoded.Bytes(), nil
}
