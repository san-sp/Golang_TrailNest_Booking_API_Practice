package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
)

type fakeS3Client struct {
	putInput    *s3.PutObjectInput
	deleteInput *s3.DeleteObjectInput

	putErr    error
	deleteErr error

	putCalls    int
	deleteCalls int
}

func (f *fakeS3Client) PutObject(
	ctx context.Context,
	input *s3.PutObjectInput,
	_ ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	f.putCalls++
	f.putInput = input

	if f.putErr != nil {
		return nil, f.putErr
	}

	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3Client) DeleteObject(
	ctx context.Context,
	input *s3.DeleteObjectInput,
	_ ...func(*s3.Options),
) (*s3.DeleteObjectOutput, error) {
	f.deleteCalls++
	f.deleteInput = input

	if f.deleteErr != nil {
		return nil, f.deleteErr
	}

	return &s3.DeleteObjectOutput{}, nil
}

func newTestS3Storage(t *testing.T, client s3Client) *S3ImageStorage {
	t.Helper()

	store, err := NewS3ImageStorage(
		client,
		"trailnest-test",
		"https://cdn.example.com/activities",
	)
	require.NoError(t, err)

	return store
}

func TestNewS3ImageStorageValidation(t *testing.T) {
	tests := []struct {
		name      string
		client    s3Client
		bucket    string
		publicURL string
	}{
		{
			name:      "nil client",
			bucket:    "trailnest",
			publicURL: "https://cdn.example.com",
		},
		{
			name:      "empty bucket",
			client:    &fakeS3Client{},
			publicURL: "https://cdn.example.com",
		},
		{
			name:   "empty public URL",
			client: &fakeS3Client{},
			bucket: "trailnest",
		},
		{
			name:      "invalid public URL",
			client:    &fakeS3Client{},
			bucket:    "trailnest",
			publicURL: "ftp://cdn.example.com",
		},
		{
			name:      "public URL with empty query",
			client:    &fakeS3Client{},
			bucket:    "trailnest",
			publicURL: "https://cdn.example.com?",
		},
		{
			name:      "public URL with empty fragment",
			client:    &fakeS3Client{},
			bucket:    "trailnest",
			publicURL: "https://cdn.example.com#",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewS3ImageStorage(
				tt.client,
				tt.bucket,
				tt.publicURL,
			)

			require.Error(t, err)
		})
	}
}

func TestS3ImageStorageSave(t *testing.T) {
	client := &fakeS3Client{}
	store := newTestS3Storage(t, client)

	file := multipartFileFromReader(t, makePNG(t))

	imageURL, err := store.Save(context.Background(), file)
	require.NoError(t, err)

	require.Equal(t, 1, client.putCalls)
	require.Equal(t, "trailnest-test", aws.ToString(client.putInput.Bucket))
	require.Equal(t, "image/webp", aws.ToString(client.putInput.ContentType))

	key := aws.ToString(client.putInput.Key)
	require.True(t, isValidImageKey(key))

	require.Equal(
		t,
		"https://cdn.example.com/activities/"+key,
		imageURL,
	)

	uploadedBytes, err := io.ReadAll(client.putInput.Body)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(uploadedBytes), 12)

	require.Equal(t, "RIFF", string(uploadedBytes[:4]))
	require.Equal(t, "WEBP", string(uploadedBytes[8:12]))
}

func TestS3ImageStorageSaveReturnsUploadError(t *testing.T) {
	expectedErr := errors.New("S3 upload failed")

	client := &fakeS3Client{
		putErr: expectedErr,
	}

	store := newTestS3Storage(t, client)
	file := multipartFileFromReader(t, makePNG(t))

	_, err := store.Save(context.Background(), file)

	require.ErrorIs(t, err, expectedErr)
	require.Equal(t, 1, client.putCalls)
}

func TestS3ImageStorageSaveRejectsInvalidImage(t *testing.T) {
	client := &fakeS3Client{}
	store := newTestS3Storage(t, client)

	file := multipartFileFromReader(
		t,
		bytes.NewReader([]byte("not an image")),
	)

	_, err := store.Save(context.Background(), file)

	require.Error(t, err)
	require.Equal(t, 0, client.putCalls)
}

func TestS3ImageStorageDelete(t *testing.T) {
	client := &fakeS3Client{}
	store := newTestS3Storage(t, client)

	imageURL := "https://cdn.example.com/activities/0123456789abcdef0123456789abcdef.webp"

	err := store.Delete(context.Background(), imageURL)
	require.NoError(t, err)

	require.Equal(t, 1, client.deleteCalls)
	require.Equal(t, "trailnest-test", aws.ToString(client.deleteInput.Bucket))
	require.Equal(
		t,
		"0123456789abcdef0123456789abcdef.webp",
		aws.ToString(client.deleteInput.Key),
	)
}

func TestS3ImageStorageDeleteRejectsInvalidURLs(t *testing.T) {
	client := &fakeS3Client{}
	store := newTestS3Storage(t, client)

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "different host",
			url:  "https://attacker.example.com/activities/0123456789abcdef0123456789abcdef.webp",
		},
		{
			name: "different path",
			url:  "https://cdn.example.com/other/0123456789abcdef0123456789abcdef.webp",
		},
		{
			name: "invalid filename",
			url:  "https://cdn.example.com/activities/malicious.webp",
		},
		{
			name: "query string",
			url:  "https://cdn.example.com/activities/0123456789abcdef0123456789abcdef.webp?other=1",
		},
		{
			name: "path traversal",
			url:  "https://cdn.example.com/activities/../secret.webp",
		},
		{
			name: "encoded path traversal",
			url:  "https://cdn.example.com/activities/%2e%2e%2fsecret.webp",
		},
		{
			name: "empty query delimiter",
			url:  "https://cdn.example.com/activities/0123456789abcdef0123456789abcdef.webp?",
		},
		{
			name: "empty fragment delimiter",
			url:  "https://cdn.example.com/activities/0123456789abcdef0123456789abcdef.webp#",
		},
		{
			name: "encoded filename",
			url:  "https://cdn.example.com/activities/%30%31%32%33%34%35%36%37%38%39%61%62%63%64%65%66%30%31%32%33%34%35%36%37%38%39%61%62%63%64%65%66%2ewebp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.Delete(context.Background(), tt.url)

			require.ErrorIs(t, err, ErrInvalidImageURL)
		})
	}

	require.Equal(t, 0, client.deleteCalls)
}

func TestS3ImageStorageDeleteReturnsS3Error(t *testing.T) {
	expectedErr := errors.New("S3 deletion failed")

	client := &fakeS3Client{
		deleteErr: expectedErr,
	}

	store := newTestS3Storage(t, client)

	imageURL := "https://cdn.example.com/activities/0123456789abcdef0123456789abcdef.webp"

	err := store.Delete(context.Background(), imageURL)

	require.ErrorIs(t, err, expectedErr)
	require.Equal(t, 1, client.deleteCalls)
}

func TestS3ImageStorageRespectsCancelledContext(t *testing.T) {
	client := &fakeS3Client{}
	store := newTestS3Storage(t, client)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	file := multipartFileFromReader(t, makePNG(t))

	_, err := store.Save(ctx, file)
	require.ErrorIs(t, err, context.Canceled)

	err = store.Delete(
		ctx,
		"https://cdn.example.com/activities/0123456789abcdef0123456789abcdef.webp",
	)
	require.ErrorIs(t, err, context.Canceled)

	require.Equal(t, 0, client.putCalls)
	require.Equal(t, 0, client.deleteCalls)
}
