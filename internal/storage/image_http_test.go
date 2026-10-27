package storage

import (
	"bytes"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/KarpelesLab/gowebp"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGinServesStoredWebPImage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	filename := "test-image.webp"
	path := filepath.Join(dir, filename)

	// Generate a real WebP image for the static-file test.
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var encoded bytes.Buffer

	require.NoError(t, gowebp.Encode(&encoded, img, &gowebp.Options{
		Lossy:   true,
		Quality: 88,
		Method:  5,
	}))
	require.NoError(t, os.WriteFile(path, encoded.Bytes(), 0600))

	router := gin.New()
	router.Static("/uploads/activities", dir)

	request := httptest.NewRequest(
		http.MethodGet,
		"/uploads/activities/"+filename,
		nil,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Header().Get("Content-Type"), "image/webp")
	require.Equal(t, encoded.Bytes(), recorder.Body.Bytes())
}
