package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var ErrInvalidImageURL = errors.New("invalid image URL")

// s3Client describes the S3 operations required by our storage adapter.
// Keeping this interface small allows us to mock S3 in unit tests.
type s3Client interface {
	PutObject(
		ctx context.Context,
		params *s3.PutObjectInput,
		optFns ...func(*s3.Options),
	) (*s3.PutObjectOutput, error)

	DeleteObject(
		ctx context.Context,
		params *s3.DeleteObjectInput,
		optFns ...func(*s3.Options),
	) (*s3.DeleteObjectOutput, error)
}

// S3ImageStorage stores processed images in an S3-compatible bucket.
type S3ImageStorage struct {
	client    s3Client
	bucket    string
	publicURL *url.URL
}

// NewS3ImageStorage creates an S3 image storage adapter.
func NewS3ImageStorage(
	client s3Client,
	bucket string,
	publicURL string,
) (*S3ImageStorage, error) {
	if client == nil {
		return nil, errors.New("S3 client is required")
	}

	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return nil, errors.New("S3 bucket is required")
	}

	parsedURL, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil {
		return nil, fmt.Errorf("parse image public URL: %w", err)
	}

	if (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") ||
		parsedURL.Host == "" ||
		parsedURL.User != nil ||
		parsedURL.RawQuery != "" ||
		parsedURL.ForceQuery ||
		parsedURL.Fragment != "" ||
		strings.Contains(publicURL, "#") {
		return nil, errors.New("invalid image public URL")
	}

	parsedURL.Path = strings.TrimRight(parsedURL.Path, "/")
	parsedURL.RawPath = ""

	return &S3ImageStorage{
		client:    client,
		bucket:    bucket,
		publicURL: parsedURL,
	}, nil
}

// Save processes an uploaded image and stores the resulting WebP object in S3.
func (s *S3ImageStorage) Save(
	ctx context.Context,
	file multipart.File,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

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

	key := hex.EncodeToString(randomName[:]) + ".webp"

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(encoded),
		ContentType: aws.String("image/webp"),
	})
	if err != nil {
		return "", fmt.Errorf("upload image to S3: %w", err)
	}

	publicURL := *s.publicURL
	publicURL.Path = path.Join(publicURL.Path, key)

	return publicURL.String(), nil
}

// Delete removes an image previously stored by this adapter.
func (s *S3ImageStorage) Delete(
	ctx context.Context,
	imageURL string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	parsedURL, err := url.Parse(imageURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidImageURL, err)
	}

	if !strings.EqualFold(parsedURL.Scheme, s.publicURL.Scheme) ||
		!strings.EqualFold(parsedURL.Host, s.publicURL.Host) ||
		parsedURL.User != nil ||
		parsedURL.RawQuery != "" ||
		parsedURL.ForceQuery ||
		parsedURL.Fragment != "" ||
		parsedURL.RawFragment != "" ||
		strings.Contains(imageURL, "#") {
		return ErrInvalidImageURL
	}

	if parsedURL.RawPath != "" {
		return ErrInvalidImageURL
	}

	key := strings.TrimPrefix(
		parsedURL.Path,
		s.publicURL.Path+"/",
	)

	if key == parsedURL.Path || !isValidImageKey(key) {
		return ErrInvalidImageURL
	}

	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete image from S3: %w", err)
	}

	return nil
}

// isValidImageKey accepts only our generated 32-character hexadecimal
// filenames with the .webp extension.
func isValidImageKey(key string) bool {
	if len(key) != 37 || !strings.HasSuffix(key, ".webp") {
		return false
	}

	_, err := hex.DecodeString(strings.TrimSuffix(key, ".webp"))
	return err == nil
}
