package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/KarpelesLab/gowebp"
	xdraw "golang.org/x/image/draw"
)

const (
	MaxImageSize   int64 = 5 << 20
	MaxImageWidth        = 1600
	MaxImageHeight       = 1600
	MaxImagePixels int64 = 40_000_000
)

var (
	ErrImageTooLarge   = errors.New("image exceeds maximum size")
	ErrImageDimensions = errors.New("image dimensions exceed maximum")
)

type LocalImageStorage struct {
	directory string
	publicURL string
}

func NewLocalImageStorage(directory, publicURL string) (*LocalImageStorage, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("image storage directory is required")
	}
	if strings.TrimSpace(publicURL) == "" {
		return nil, errors.New("image storage public URL is required")
	}

	if err := os.MkdirAll(directory, 0750); err != nil {
		return nil, fmt.Errorf("create image storage directory: %w", err)
	}

	return &LocalImageStorage{
		directory: directory,
		publicURL: strings.TrimRight(publicURL, "/"),
	}, nil
}

func (s *LocalImageStorage) Save(
	ctx context.Context,
	file multipart.File,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	data, err := io.ReadAll(io.LimitReader(file, MaxImageSize+1))
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}
	if int64(len(data)) > MaxImageSize {
		return "", ErrImageTooLarge
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("invalid image content: %w", err)
	}
	if format != "jpeg" && format != "png" {
		return "", fmt.Errorf("unsupported image format: %s", format)
	}
	if config.Width <= 0 || config.Height <= 0 ||
		int64(config.Width) > MaxImagePixels/int64(config.Height) {
		return "", ErrImageDimensions
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("decode image: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return "", err
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
		return "", fmt.Errorf("encode WebP image: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}

	var randomName [16]byte
	if _, err := rand.Read(randomName[:]); err != nil {
		return "", fmt.Errorf("generate image filename: %w", err)
	}

	filename := hex.EncodeToString(randomName[:]) + ".webp"
	path := filepath.Join(s.directory, filename)

	if err := os.WriteFile(path, encoded.Bytes(), 0640); err != nil {
		return "", fmt.Errorf("write image: %w", err)
	}

	return s.publicURL + "/" + filename, nil
}

func (s *LocalImageStorage) Delete(
	ctx context.Context,
	imageURL string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	prefix := s.publicURL + "/"
	if !strings.HasPrefix(imageURL, prefix) {
		return errors.New("image URL does not belong to this storage")
	}

	filename := strings.TrimPrefix(imageURL, prefix)

	// Only permit generated filenames, never arbitrary filesystem paths.
	if len(filename) != 37 ||
		!strings.HasSuffix(filename, ".webp") {
		return errors.New("invalid stored image filename")
	}

	if _, err := hex.DecodeString(strings.TrimSuffix(filename, ".webp")); err != nil {
		return errors.New("invalid stored image filename")
	}

	path := filepath.Join(s.directory, filename)

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete image: %w", err)
	}

	return nil
}
