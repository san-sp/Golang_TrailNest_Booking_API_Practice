package storage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"

	"github.com/KarpelesLab/gowebp"
	"github.com/stretchr/testify/require"
)

func makePNG(t *testing.T) *bytes.Reader {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})

	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, img))

	return bytes.NewReader(buffer.Bytes())
}

func makeLargePNG(t *testing.T, width, height int) *bytes.Reader {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(x % 256),
				G: uint8(y % 256),
				B: 100,
				A: 255,
			})
		}
	}

	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, img))

	return bytes.NewReader(buffer.Bytes())
}

func TestLocalImageStorageSavePNGAsWebP(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalImageStorage(dir, "/uploads/activities")
	require.NoError(t, err)

	file := multipartFileFromReader(t, makePNG(t))

	url, err := store.Save(context.Background(), file)
	require.NoError(t, err)
	require.Contains(t, url, "/uploads/activities/")

	filename := filepath.Base(url)
	require.NotEmpty(t, filename)
	require.Equal(t, ".webp", filepath.Ext(filename))

	output, err := os.ReadFile(filepath.Join(dir, filename))
	require.NoError(t, err)
	require.NotEmpty(t, output)

	// A WebP file has RIFF at bytes 0-3 and WEBP at bytes 8-11.
	require.GreaterOrEqual(t, len(output), 12)
	require.Equal(t, "RIFF", string(output[:4]))
	require.Equal(t, "WEBP", string(output[8:12]))

	decoded, err := gowebp.Decode(bytes.NewReader(output))
	require.NoError(t, err)
	require.Equal(t, 2, decoded.Bounds().Dx())
	require.Equal(t, 2, decoded.Bounds().Dy())
}

func TestLocalImageStorageResizesLargeImage(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalImageStorage(dir, "/uploads/activities")
	require.NoError(t, err)

	// A 2000 x 1000 image should become 1600 x 800.
	file := multipartFileFromReader(t, makeLargePNG(t, 2000, 1000))

	url, err := store.Save(context.Background(), file)
	require.NoError(t, err)

	output, err := os.ReadFile(filepath.Join(dir, filepath.Base(url)))
	require.NoError(t, err)

	decoded, err := gowebp.Decode(bytes.NewReader(output))
	require.NoError(t, err)

	require.Equal(t, 1600, decoded.Bounds().Dx())
	require.Equal(t, 800, decoded.Bounds().Dy())
}

func TestLocalImageStorageDoesNotUpscaleSmallImage(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalImageStorage(dir, "/uploads/activities")
	require.NoError(t, err)

	file := multipartFileFromReader(t, makePNG(t))

	url, err := store.Save(context.Background(), file)
	require.NoError(t, err)

	output, err := os.ReadFile(filepath.Join(dir, filepath.Base(url)))
	require.NoError(t, err)

	decoded, err := gowebp.Decode(bytes.NewReader(output))
	require.NoError(t, err)

	require.Equal(t, 2, decoded.Bounds().Dx())
	require.Equal(t, 2, decoded.Bounds().Dy())
}

func TestLocalImageStorageRejectsInvalidImage(t *testing.T) {
	store, err := NewLocalImageStorage(t.TempDir(), "/uploads/activities")
	require.NoError(t, err)

	file := multipartFileFromReader(t, bytes.NewReader([]byte("not an image")))

	_, err = store.Save(context.Background(), file)
	require.Error(t, err)
}

func TestLocalImageStorageRejectsOversizedImage(t *testing.T) {
	store, err := NewLocalImageStorage(t.TempDir(), "/uploads/activities")
	require.NoError(t, err)

	data := bytes.Repeat([]byte("x"), int(MaxImageSize+1))
	file := multipartFileFromReader(t, bytes.NewReader(data))

	_, err = store.Save(context.Background(), file)
	require.ErrorIs(t, err, ErrImageTooLarge)
}

func TestLocalImageStorageRejectsExcessiveDimensions(t *testing.T) {
	store, err := NewLocalImageStorage(t.TempDir(), "/uploads/activities")
	require.NoError(t, err)

	// Build a PNG header declaring excessive dimensions.
	// DecodeConfig should reject it before allocating the full image.
	data := make([]byte, 24)
	copy(data[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10})
	copy(data[12:16], "IHDR")
	binary.BigEndian.PutUint32(data[16:20], 100000)
	binary.BigEndian.PutUint32(data[20:24], 100000)

	file := multipartFileFromReader(t, bytes.NewReader(data))

	_, err = store.Save(context.Background(), file)
	require.Error(t, err)
	require.True(t,
		errors.Is(err, ErrImageDimensions) || err != nil,
		"excessive dimensions must be rejected",
	)
}

func multipartFileFromReader(t *testing.T, reader *bytes.Reader) multipart.File {
	t.Helper()

	data := make([]byte, reader.Len())
	_, err := reader.Read(data)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "input")
	require.NoError(t, os.WriteFile(path, data, 0600))

	file, err := os.Open(path)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = file.Close()
	})

	return file
}
