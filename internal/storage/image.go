package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
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
	encoded, err := processImage(ctx, file)
	if err != nil {
		return "", err
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

	if err := os.WriteFile(path, encoded, 0640); err != nil {
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
