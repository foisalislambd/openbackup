package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/openbackup/openbackup/internal/hash"
)

// S3Config configures the object storage backend.
type S3Config struct {
	// Endpoint is the host, for example "s3.amazonaws.com" or "minio:9000".
	Endpoint string
	Bucket   string
	// Prefix namespaces the objects inside the bucket, so one bucket can hold
	// several deployments.
	Prefix    string
	AccessKey string
	SecretKey string
	Region    string
	UseSSL    bool
}

// S3Blobs stores blobs in an S3-compatible object store.
type S3Blobs struct {
	client *minio.Client
	cfg    S3Config
}

var _ Blobs = (*S3Blobs)(nil)

// NewS3Blobs connects to object storage and ensures the bucket exists.
func NewS3Blobs(ctx context.Context, cfg S3Config) (*S3Blobs, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("store: S3 endpoint and bucket are required")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("store: connect to object storage: %w", err)
	}
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("store: check bucket %q: %w", cfg.Bucket, err)
	}
	if !exists {
		// Creating the bucket keeps the one-command install working against a
		// fresh MinIO container.
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, fmt.Errorf("store: create bucket %q: %w", cfg.Bucket, err)
		}
	}
	cfg.Prefix = strings.Trim(cfg.Prefix, "/")
	return &S3Blobs{client: client, cfg: cfg}, nil
}

// Location describes the bucket for logs and the dashboard.
func (b *S3Blobs) Location() string {
	if b.cfg.Prefix != "" {
		return fmt.Sprintf("s3://%s/%s (%s)", b.cfg.Bucket, b.cfg.Prefix, b.cfg.Endpoint)
	}
	return fmt.Sprintf("s3://%s (%s)", b.cfg.Bucket, b.cfg.Endpoint)
}

// FreeBytes is unknowable for object storage, which is effectively unbounded.
func (b *S3Blobs) FreeBytes() int64 { return -1 }

// key mirrors the filesystem sharding so a bucket can be synced to disk and
// back without rewriting object names.
func (b *S3Blobs) key(digest string) string {
	parts := []string{"blobs", digest[0:2], digest[2:4], digest}
	if b.cfg.Prefix != "" {
		parts = append([]string{b.cfg.Prefix}, parts...)
	}
	return strings.Join(parts, "/")
}

// Has reports whether an object exists.
func (b *S3Blobs) Has(ctx context.Context, digest string) (int64, bool) {
	if err := hash.Validate(digest); err != nil {
		return 0, false
	}
	info, err := b.client.StatObject(ctx, b.cfg.Bucket, b.key(digest), minio.StatObjectOptions{})
	if err != nil {
		return 0, false
	}
	return info.Size, true
}

// Put uploads a blob unless it is already present.
func (b *S3Blobs) Put(ctx context.Context, digest string, blob []byte) (int64, error) {
	if err := hash.Validate(digest); err != nil {
		return 0, err
	}
	if _, exists := b.Has(ctx, digest); exists {
		return 0, nil
	}
	_, err := b.client.PutObject(ctx, b.cfg.Bucket, b.key(digest), bytes.NewReader(blob), int64(len(blob)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"})
	if err != nil {
		return 0, fmt.Errorf("store: upload blob: %w", err)
	}
	return int64(len(blob)), nil
}

// Get downloads a whole blob.
func (b *S3Blobs) Get(ctx context.Context, digest string) ([]byte, error) {
	rc, size, err := b.Open(ctx, digest)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	buf := bytes.NewBuffer(make([]byte, 0, max(size, 0)))
	if _, err := io.Copy(buf, rc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Open streams a blob.
func (b *S3Blobs) Open(ctx context.Context, digest string) (io.ReadCloser, int64, error) {
	if err := hash.Validate(digest); err != nil {
		return nil, 0, err
	}
	obj, err := b.client.GetObject(ctx, b.cfg.Bucket, b.key(digest), minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, translateS3Error(err)
	}
	// GetObject is lazy, so the first Stat is what surfaces a missing object.
	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, 0, translateS3Error(err)
	}
	return obj, info.Size, nil
}

// Delete removes an object.
func (b *S3Blobs) Delete(ctx context.Context, digest string) error {
	if err := hash.Validate(digest); err != nil {
		return err
	}
	err := b.client.RemoveObject(ctx, b.cfg.Bucket, b.key(digest), minio.RemoveObjectOptions{})
	if err != nil && !errors.Is(translateS3Error(err), ErrBlobNotFound) {
		return err
	}
	return nil
}

// Walk lists every stored object.
func (b *S3Blobs) Walk(ctx context.Context, fn func(digest string, size int64) error) error {
	prefix := "blobs/"
	if b.cfg.Prefix != "" {
		prefix = b.cfg.Prefix + "/blobs/"
	}
	for obj := range b.client.ListObjects(ctx, b.cfg.Bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return obj.Err
		}
		name := obj.Key
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		if hash.Validate(name) != nil {
			continue
		}
		if err := fn(name, obj.Size); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// translateS3Error maps a missing object onto the shared sentinel so callers do
// not need to know which backend they are talking to.
func translateS3Error(err error) error {
	if err == nil {
		return nil
	}
	resp := minio.ToErrorResponse(err)
	if resp.StatusCode == http.StatusNotFound || resp.Code == "NoSuchKey" {
		return ErrBlobNotFound
	}
	return err
}
