// Package uploader turns a local file into deduplicated, compressed, optionally
// encrypted chunks on the server.
//
// The pipeline is: read once, split on content-defined boundaries, ask the
// server which of those chunks it already has, and upload only the rest. Asking
// in batches rather than per chunk keeps a backup of many small files from
// becoming a round-trip per chunk, while the batch's byte ceiling keeps memory
// flat regardless of file size.
package uploader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/openbackup/openbackup/internal/api"
	"github.com/openbackup/openbackup/internal/chunk"
	"github.com/openbackup/openbackup/internal/codec"
	"github.com/openbackup/openbackup/internal/hash"
	"github.com/openbackup/openbackup/internal/throttle"
)

// batch limits bound how much unwritten data is held in memory at once.
const (
	maxBatchChunks = 32
	maxBatchBytes  = 8 << 20
)

// Uploader uploads file content.
type Uploader struct {
	client *api.Client
	codec  *codec.Codec
	bucket *throttle.Bucket
	cfg    chunk.Config

	concurrency int

	// Progress is called as bytes are read and uploaded, for the CLI and tray.
	Progress func(Progress)

	stats Stats
}

// Progress reports upload movement.
type Progress struct {
	Path string
	// FileBytesRead is how much of the current file has been processed.
	FileBytesRead int64
	FileSize      int64
	// UploadedBytes is how much has gone over the network for this file.
	UploadedBytes int64
	// SkippedBytes is content the server already had, i.e. deduplicated.
	SkippedBytes int64
}

// Stats accumulates totals for one backup run.
type Stats struct {
	FilesUploaded  atomic.Int64
	ChunksUploaded atomic.Int64
	ChunksSkipped  atomic.Int64
	BytesRead      atomic.Int64
	BytesUploaded  atomic.Int64
	// BytesDeduplicated is plaintext the server already had, which is what makes
	// the second backup of a machine fast.
	BytesDeduplicated atomic.Int64
}

// Options configures an Uploader.
type Options struct {
	Client      *api.Client
	Codec       *codec.Codec
	Bucket      *throttle.Bucket
	ChunkConfig chunk.Config
	Concurrency int
}

// New builds an Uploader.
func New(opts Options) (*Uploader, error) {
	if opts.Client == nil || opts.Codec == nil {
		return nil, errors.New("uploader: client and codec are required")
	}
	cfg := opts.ChunkConfig
	if cfg.Avg == 0 {
		cfg = chunk.DefaultConfig()
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 3
	}
	return &Uploader{
		client:      opts.Client,
		codec:       opts.Codec,
		bucket:      opts.Bucket,
		cfg:         cfg,
		concurrency: concurrency,
	}, nil
}

// Stats exposes the running totals.
func (u *Uploader) Stats() *Stats { return &u.stats }

// FileResult describes an uploaded file.
type FileResult struct {
	// Chunks lists the content addresses in order; concatenating them rebuilds
	// the file.
	Chunks []string
	// Digest is the hash of the whole file, used to detect corruption and to
	// recognise renames.
	Digest string
	Size   int64
}

// UploadFile chunks and uploads one file.
//
// A file being written while it is read is the normal case on a live system, so
// a size change is reported rather than treated as an error: the caller retries
// it in the next pass, when the file has settled.
func (u *Uploader) UploadFile(ctx context.Context, path string, expectedSize int64) (*FileResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	whole := hash.NewHasher()
	chunker, err := chunk.NewChunker(io.TeeReader(f, whole), u.cfg)
	if err != nil {
		return nil, err
	}

	result := &FileResult{}
	var progress Progress
	progress.Path = path
	progress.FileSize = expectedSize

	batch := newBatch()
	flush := func() error {
		if len(batch.chunks) == 0 {
			return nil
		}
		uploaded, skipped, err := u.flushBatch(ctx, batch)
		if err != nil {
			return err
		}
		progress.UploadedBytes += uploaded
		progress.SkippedBytes += skipped
		u.report(progress)
		batch.reset()
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c, err := chunker.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("uploader: read %s: %w", path, err)
		}

		digest := hash.Sum(c.Data)
		result.Chunks = append(result.Chunks, digest)
		result.Size += int64(len(c.Data))
		progress.FileBytesRead = result.Size
		u.stats.BytesRead.Add(int64(len(c.Data)))

		// The chunker reuses its buffer, so the data must be copied before it is
		// handed to a worker goroutine.
		batch.add(digest, append([]byte(nil), c.Data...))
		if batch.full() {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}

	result.Digest = whole.Hex()
	u.stats.FilesUploaded.Add(1)
	return result, nil
}

// flushBatch uploads the chunks in a batch that the server does not already have.
func (u *Uploader) flushBatch(ctx context.Context, b *batch) (uploaded, skipped int64, err error) {
	// One question for the whole batch: "which of these do you not have?".
	missing, err := u.client.MissingChunks(ctx, b.digests())
	if err != nil {
		return 0, 0, err
	}
	needed := make(map[string]struct{}, len(missing))
	for _, d := range missing {
		needed[d] = struct{}{}
	}

	type job struct {
		digest string
		data   []byte
	}
	jobs := make(chan job)
	var (
		wg           sync.WaitGroup
		uploadedBy   atomic.Int64
		skippedBytes atomic.Int64
		firstErr     error
		errMu        sync.Mutex
	)
	fail := func(e error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = e
		}
		errMu.Unlock()
	}

	workers := min(u.concurrency, len(needed))
	if workers == 0 {
		workers = 1
	}
	uploadCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				blob, err := u.codec.Encode(j.data, j.digest)
				if err != nil {
					fail(err)
					cancel()
					return
				}
				// Throttling applies to what actually crosses the network, which
				// is the compressed size, not the original.
				if u.bucket != nil {
					if err := u.bucket.Wait(uploadCtx, len(blob)); err != nil {
						fail(err)
						return
					}
				}
				if err := u.client.PutChunk(uploadCtx, j.digest, blob, len(j.data)); err != nil {
					fail(err)
					cancel()
					return
				}
				uploadedBy.Add(int64(len(blob)))
				u.stats.BytesUploaded.Add(int64(len(blob)))
				u.stats.ChunksUploaded.Add(1)
			}
		}()
	}

	for i, digest := range b.digests() {
		if _, ok := needed[digest]; !ok {
			// The server already has this content: nothing to send.
			size := int64(len(b.chunks[i].data))
			skippedBytes.Add(size)
			u.stats.BytesDeduplicated.Add(size)
			u.stats.ChunksSkipped.Add(1)
			continue
		}
		select {
		case jobs <- job{digest: digest, data: b.chunks[i].data}:
		case <-uploadCtx.Done():
			close(jobs)
			wg.Wait()
			errMu.Lock()
			defer errMu.Unlock()
			if firstErr != nil {
				return uploadedBy.Load(), skippedBytes.Load(), firstErr
			}
			return uploadedBy.Load(), skippedBytes.Load(), uploadCtx.Err()
		}
	}
	close(jobs)
	wg.Wait()

	errMu.Lock()
	defer errMu.Unlock()
	return uploadedBy.Load(), skippedBytes.Load(), firstErr
}

func (u *Uploader) report(p Progress) {
	if u.Progress != nil {
		u.Progress(p)
	}
}

// batch holds chunks awaiting a deduplication check and upload.
type batch struct {
	chunks []struct {
		digest string
		data   []byte
	}
	bytes int
}

func newBatch() *batch { return &batch{} }

func (b *batch) add(digest string, data []byte) {
	b.chunks = append(b.chunks, struct {
		digest string
		data   []byte
	}{digest: digest, data: data})
	b.bytes += len(data)
}

func (b *batch) full() bool {
	return len(b.chunks) >= maxBatchChunks || b.bytes >= maxBatchBytes
}

func (b *batch) digests() []string {
	out := make([]string, len(b.chunks))
	for i, c := range b.chunks {
		out[i] = c.digest
	}
	return out
}

func (b *batch) reset() {
	b.chunks = b.chunks[:0]
	b.bytes = 0
}
