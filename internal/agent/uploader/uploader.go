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

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/bufpool"
	"github.com/foisalislambd/openbackup/internal/chunk"
	"github.com/foisalislambd/openbackup/internal/codec"
	"github.com/foisalislambd/openbackup/internal/hash"
	"github.com/foisalislambd/openbackup/internal/throttle"
	"golang.org/x/time/rate"
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

	// cfgMu guards cfg: the heartbeat goroutine applies the server's chunking
	// policy while a backup is reading it.
	cfgMu sync.RWMutex
	cfg   chunk.Config

	concurrency int

	// Progress is called as bytes are read and uploaded, for the CLI and tray.
	Progress func(Progress)

	stats Stats

	mu        sync.Mutex
	pending   *batch
	chunker   *chunk.Chunker
	fileHash  *hash.Hasher
	chunkHash *hash.Hasher
	// gate is checked between chunk batches so a large file still yields to
	// the governor. Nil means always proceed.
	gate func(context.Context) error
	// readLim paces source reads. Nil means unlimited.
	readLim *rate.Limiter
	// batchUnsupported remembers a server that does not accept packed uploads,
	// so the agent does not pay a failed request on every batch.
	batchUnsupported atomic.Bool
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
	chunker, err := chunk.NewChunker(nil, cfg)
	if err != nil {
		return nil, err
	}
	return &Uploader{
		client:      opts.Client,
		codec:       opts.Codec,
		bucket:      opts.Bucket,
		cfg:         cfg,
		concurrency: concurrency,
		pending:     newBatch(),
		chunker:     chunker,
		fileHash:    hash.NewHasher(),
		chunkHash:   hash.NewHasher(),
	}, nil
}

// SetGate installs a callback invoked before each chunk batch is uploaded.
// The engine uses it to apply CPU and pause policy in the middle of a large file.
func (u *Uploader) SetGate(fn func(context.Context) error) { u.gate = fn }

// SetReadLimit paces how fast file contents are read. Zero disables the limit.
func (u *Uploader) SetReadLimit(bytesPerSec int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if bytesPerSec <= 0 {
		u.readLim = nil
		return
	}
	burst := int(bytesPerSec)
	if burst < 64<<10 {
		burst = 64 << 10
	}
	u.readLim = rate.NewLimiter(rate.Limit(bytesPerSec), burst)
}

// Buffered reports how many plaintext bytes are waiting for the next upload.
func (u *Uploader) Buffered() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.pending == nil {
		return 0
	}
	return u.pending.bytes
}

// Flush uploads any chunks still held across files. The engine calls it before
// recording those files in the local index, so a crash cannot remember a file
// whose chunks never reached the server.
func (u *Uploader) Flush(ctx context.Context) error {
	u.mu.Lock()
	b := u.pending
	u.pending = newBatch()
	u.mu.Unlock()
	if b == nil || len(b.chunks) == 0 {
		return nil
	}
	_, _, err := u.flushBatch(ctx, b)
	b.release()
	return err
}

// ReleaseMemory drops buffers kept warm between files. Called after a backup
// so an idle agent does not retain the last run's windows.
func (u *Uploader) ReleaseMemory() {
	u.mu.Lock()
	if u.chunker != nil {
		u.chunker.SetBuffer(nil)
	}
	if u.pending != nil {
		u.pending.release()
	}
	u.mu.Unlock()
	if u.codec != nil {
		u.codec.Trim()
	}
	bufpool.Release()
}

// SetChunkConfig swaps the chunking parameters, which is how a server-side
// policy change reaches running uploads. The next file picks it up; the file
// currently being chunked keeps the settings it started with.
func (u *Uploader) SetChunkConfig(cfg chunk.Config) {
	u.cfgMu.Lock()
	defer u.cfgMu.Unlock()
	u.cfg = cfg
}

func (u *Uploader) chunkConfig() chunk.Config {
	u.cfgMu.RLock()
	defer u.cfgMu.RUnlock()
	return u.cfg
}

// Stats exposes the running totals for the current backup.
func (u *Uploader) Stats() *Stats { return &u.stats }

// ResetStats clears per-run counters so the next backup reports only its own
// uploads. Without this, CompleteSnapshot would store a cumulative total and
// the dashboard would show every later backup as if it re-uploaded old data.
func (u *Uploader) ResetStats() {
	u.stats.FilesUploaded.Store(0)
	u.stats.ChunksUploaded.Store(0)
	u.stats.ChunksSkipped.Store(0)
	u.stats.BytesRead.Store(0)
	u.stats.BytesUploaded.Store(0)
	u.stats.BytesDeduplicated.Store(0)
}

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

// UploadFile chunks one file and queues its chunks. Small files share a batch
// with their neighbours; Flush sends whatever is still queued. Chunk boundaries
// are unchanged from a per-file chunker: only the buffer and the upload
// grouping differ.
//
// A file being written while it is read is the normal case on a live system:
// whatever bytes were read are hashed and stored consistently, and a file that
// kept changing shows up as changed in the next pass, when it has settled.
func (u *Uploader) UploadFile(ctx context.Context, path string, expectedSize int64) (*FileResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cfg := u.chunkConfig()
	window := bufpool.Get(chunk.BufferSize(cfg, expectedSize))
	u.chunker.SetBuffer(window)
	defer func() {
		// Drop the chunker's reference before the buffer goes back into the
		// pool. Otherwise the next Get can alias a window the chunker still
		// believes it owns.
		u.chunker.SetBuffer(nil)
		bufpool.Put(window[:0])
	}()

	u.fileHash.Reset()
	var src io.Reader = f
	u.mu.Lock()
	lim := u.readLim
	u.mu.Unlock()
	if lim != nil {
		src = &pacedReader{ctx: ctx, r: f, lim: lim}
	}
	u.chunker.Reset(io.TeeReader(src, u.fileHash))

	result := &FileResult{}
	var progress Progress
	progress.Path = path
	progress.FileSize = expectedSize

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c, err := u.chunker.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("uploader: read %s: %w", path, err)
		}
		u.chunkHash.Reset()
		if _, err := u.chunkHash.Write(c.Data); err != nil {
			return nil, err
		}
		digest := u.chunkHash.Hex()
		result.Chunks = append(result.Chunks, digest)
		result.Size += int64(len(c.Data))
		progress.FileBytesRead = result.Size
		u.stats.BytesRead.Add(int64(len(c.Data)))

		// The chunker reuses its window on the next Next call, so the bytes
		// have to be copied before the batch is handed to an encoder.
		copied := bufpool.Get(len(c.Data))
		copy(copied, c.Data)
		// enqueue takes ownership of copied. A failed flush returns it; putting
		// it here as well would place the same storage in the pool twice.
		if err := u.enqueue(ctx, digest, copied, &progress); err != nil {
			return nil, err
		}
	}
	result.Digest = u.fileHash.Hex()
	u.stats.FilesUploaded.Add(1)
	return result, nil
}

func (u *Uploader) enqueue(ctx context.Context, digest string, data []byte, progress *Progress) error {
	u.mu.Lock()
	if u.pending.ready() {
		flush := u.pending
		u.pending = newBatch()
		u.mu.Unlock()
		if err := u.finishFlush(ctx, flush, progress); err != nil {
			// data was not adopted by the flushed batch.
			bufpool.Put(data[:0])
			return err
		}
		u.mu.Lock()
	}
	u.pending.add(digest, data)
	u.mu.Unlock()
	return nil
}

func (u *Uploader) finishFlush(ctx context.Context, flush *batch, progress *Progress) error {
	uploaded, skipped, err := u.flushBatch(ctx, flush)
	flush.release()
	if err != nil {
		return err
	}
	progress.UploadedBytes += uploaded
	progress.SkippedBytes += skipped
	u.report(*progress)
	return nil
}

type pacedReader struct {
	ctx context.Context
	r   io.Reader
	lim *rate.Limiter
}

func (p *pacedReader) Read(buf []byte) (int, error) {
	if len(buf) > 64<<10 {
		buf = buf[:64<<10]
	}
	if len(buf) == 0 {
		return 0, nil
	}
	if err := p.lim.WaitN(p.ctx, len(buf)); err != nil {
		return 0, err
	}
	return p.r.Read(buf)
}

// flushBatch uploads the chunks in a batch that the server does not already have.
func (u *Uploader) flushBatch(ctx context.Context, b *batch) (uploaded, skipped int64, err error) {
	if u.gate != nil {
		if err := u.gate(ctx); err != nil {
			return 0, 0, err
		}
	}
	missing, err := u.client.MissingChunks(ctx, b.digests())
	if err != nil {
		return 0, 0, err
	}
	needed := make(map[string]struct{}, len(missing))
	for _, d := range missing {
		needed[d] = struct{}{}
	}
	sent := make(map[string]struct{}, len(missing))

	type job struct {
		index  int
		digest string
		data   []byte
	}
	var jobs []job
	var skippedBytes atomic.Int64
	for i, digest := range b.digests() {
		size := int64(len(b.chunks[i].data))
		if _, ok := needed[digest]; !ok {
			skippedBytes.Add(size)
			u.stats.BytesDeduplicated.Add(size)
			u.stats.ChunksSkipped.Add(1)
			continue
		}
		if _, dup := sent[digest]; dup {
			skippedBytes.Add(size)
			u.stats.BytesDeduplicated.Add(size)
			u.stats.ChunksSkipped.Add(1)
			continue
		}
		sent[digest] = struct{}{}
		jobs = append(jobs, job{index: i, digest: digest, data: b.chunks[i].data})
	}
	if len(jobs) == 0 {
		return 0, skippedBytes.Load(), nil
	}

	type encoded struct {
		blob     api.ChunkBlob
		wire     []byte
		slot     *codec.EncodeSlot
		plainLen int
	}
	encodedCh := make(chan encoded, len(jobs))
	jobCh := make(chan job, len(jobs))
	var (
		wg       sync.WaitGroup
		firstErr error
		errMu    sync.Mutex
	)
	fail := func(e error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = e
		}
		errMu.Unlock()
	}
	workers := min(u.concurrency, len(jobs))
	if workers < 1 {
		workers = 1
	}
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slot := u.codec.Acquire()
			defer u.codec.Release(slot)
			for j := range jobCh {
				blob, err := u.codec.EncodeReuse(slot, j.data, j.digest)
				if err != nil {
					fail(err)
					return
				}
				// EncodeReuse aliases the slot buffer, which the next chunk
				// overwrites, so the wire bytes are copied once here.
				wire := append([]byte(nil), blob...)
				encodedCh <- encoded{
					blob: api.ChunkBlob{Digest: j.digest, PlainLen: len(j.data), Blob: wire},
					wire: wire, plainLen: len(j.data),
				}
			}
		}()
	}
	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)
	wg.Wait()
	close(encodedCh)
	errMu.Lock()
	err = firstErr
	errMu.Unlock()
	if err != nil {
		return 0, skippedBytes.Load(), err
	}

	blobs := make([]api.ChunkBlob, 0, len(jobs))
	var wireBytes int64
	for item := range encodedCh {
		blobs = append(blobs, item.blob)
		wireBytes += int64(len(item.wire))
	}
	if u.bucket != nil {
		if err := u.bucket.Wait(ctx, int(wireBytes)); err != nil {
			return 0, skippedBytes.Load(), err
		}
	}
	if err := u.putEncoded(ctx, blobs); err != nil {
		return 0, skippedBytes.Load(), err
	}
	u.stats.BytesUploaded.Add(wireBytes)
	u.stats.ChunksUploaded.Add(int64(len(blobs)))
	return wireBytes, skippedBytes.Load(), nil
}

func (u *Uploader) putEncoded(ctx context.Context, blobs []api.ChunkBlob) error {
	if len(blobs) == 1 || u.batchUnsupported.Load() {
		for _, blob := range blobs {
			if err := u.client.PutChunk(ctx, blob.Digest, blob.Blob, blob.PlainLen); err != nil {
				return err
			}
		}
		return nil
	}
	err := u.client.PutChunks(ctx, blobs)
	if err == nil {
		return nil
	}
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && (apiErr.StatusCode == 404 || apiErr.StatusCode == 405) {
		u.batchUnsupported.Store(true)
		for _, blob := range blobs {
			if err := u.client.PutChunk(ctx, blob.Digest, blob.Blob, blob.PlainLen); err != nil {
				return err
			}
		}
		return nil
	}
	return err
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

func (b *batch) ready() bool {
	return len(b.chunks) >= maxBatchChunks || b.bytes >= maxBatchBytes
}

func (b *batch) digests() []string {
	out := make([]string, len(b.chunks))
	for i, c := range b.chunks {
		out[i] = c.digest
	}
	return out
}

func (b *batch) release() {
	for i := range b.chunks {
		if b.chunks[i].data != nil {
			bufpool.Put(b.chunks[i].data[:0])
			b.chunks[i].data = nil
		}
	}
	b.chunks = b.chunks[:0]
	b.bytes = 0
}
