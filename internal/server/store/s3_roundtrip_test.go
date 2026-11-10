package store

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/foisalislambd/openbackup/internal/hash"
)

// TestS3CompatibleRoundTrip talks to a local S3-compatible server (MinIO in
// CI). It does not contact AWS. The default test run skips it; the S3 CI job
// sets OB_S3_REQUIRED=1 and fails if the endpoint is missing.
func TestS3CompatibleRoundTrip(t *testing.T) {
	endpoint := os.Getenv("OB_S3_ENDPOINT")
	if endpoint == "" {
		if os.Getenv("OB_S3_REQUIRED") == "1" {
			t.Fatal("OB_S3_ENDPOINT is required in this job")
		}
		t.Skip("set OB_S3_ENDPOINT to a local S3-compatible server; this test does not use AWS")
	}
	ctx := context.Background()
	blobs, err := NewS3Blobs(ctx, S3Config{
		Endpoint:  endpoint,
		Bucket:    envOr("OB_S3_BUCKET", "openbackup-test"),
		Prefix:    "roundtrip",
		AccessKey: envOr("OB_S3_ACCESS_KEY", "minio"),
		SecretKey: envOr("OB_S3_SECRET_KEY", "minio123456"),
		Region:    envOr("OB_S3_REGION", "us-east-1"),
		UseSSL:    os.Getenv("OB_S3_USE_SSL") == "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	small := []byte("s3-small")
	large := bytes.Repeat([]byte("L"), 600<<10)
	for _, payload := range [][]byte{small, large} {
		digest := hash.Sum(payload)
		n, err := blobs.Put(ctx, digest, payload)
		if err != nil || n != int64(len(payload)) {
			t.Fatalf("put %d: n=%d err=%v", len(payload), n, err)
		}
		got, err := blobs.Get(ctx, digest)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("get %d: %v", len(payload), err)
		}
		if _, err := blobs.Put(ctx, digest, payload); err != nil {
			t.Fatal(err)
		}
	}
	digest := hash.Sum(small)
	if err := blobs.Delete(ctx, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := blobs.Get(ctx, digest); err == nil {
		t.Fatal("deleted object was still readable")
	}
	if _, err := blobs.Get(ctx, hash.Sum(large)); err != nil {
		t.Fatalf("large object after the other delete: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
