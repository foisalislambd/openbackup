package maintenance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/foisalislambd/openbackup/internal/codec"
	"github.com/foisalislambd/openbackup/internal/server/store"
)

// VerifySample reads up to limit blobs and checks that each one matches its
// record. Unencrypted blobs are decoded and hashed. Encrypted blobs are only
// checked for presence, size and a sealed header: the server does not have the
// key, and the agent verifies the digest on restore.
func (r *Runner) VerifySample(ctx context.Context, limit int) (store.IntegrityRun, error) {
	started := time.Now().UTC()
	chunks, err := r.db.SampleChunks(ctx, limit)
	if err != nil {
		return store.IntegrityRun{}, err
	}
	dec, err := codec.New(codec.Options{})
	if err != nil {
		return store.IntegrityRun{}, err
	}
	defer dec.Close()

	run := store.IntegrityRun{StartedAt: started, Checked: len(chunks)}
	var failedDigests []string
	var notes []string
	for _, chunk := range chunks {
		if ctx.Err() != nil {
			return run, ctx.Err()
		}
		ok, detail := r.checkChunk(ctx, dec, chunk)
		if err := r.db.RecordChunkCheck(ctx, chunk.Digest, ok, detail); err != nil {
			return run, err
		}
		if ok {
			continue
		}
		run.Failed++
		if len(failedDigests) < 32 {
			failedDigests = append(failedDigests, chunk.Digest)
		}
		if len(notes) < 5 {
			notes = append(notes, chunk.Digest[:8]+": "+detail)
		}
		r.log.Error("integrity check failed", "digest", chunk.Digest, "detail", detail)
	}
	run.FinishedAt = time.Now().UTC()
	if len(notes) > 0 {
		run.Detail = strings.Join(notes, "; ")
	}
	if run.Checked == 0 {
		return run, nil
	}
	if err := r.db.SaveIntegrityRun(ctx, run); err != nil {
		return run, err
	}
	if run.Failed > 0 {
		users, err := r.db.UsersOwningDigests(ctx, failedDigests)
		if err != nil {
			return run, err
		}
		msg := fmt.Sprintf("Stored data failed an integrity check (%d of %d blocks)", run.Failed, run.Checked)
		for _, userID := range users {
			if err := r.db.RaiseIntegrityAlert(ctx, userID, msg); err != nil {
				r.log.Warn("integrity alert", "user", userID, "error", err)
			}
		}
	}
	return run, nil
}

func (r *Runner) checkChunk(ctx context.Context, dec *codec.Codec, chunk store.ChunkSample) (bool, string) {
	blob, err := r.blobs.Get(ctx, chunk.Digest)
	if err != nil {
		if errors.Is(err, store.ErrBlobNotFound) {
			return false, "blob missing"
		}
		return false, err.Error()
	}
	if chunk.StoredBytes > 0 && int64(len(blob)) != chunk.StoredBytes {
		return false, "stored size does not match the index"
	}
	sealed := codec.IsEncrypted(blob)
	if sealed != chunk.Encrypted {
		return false, "encryption flag does not match the blob"
	}
	if sealed {
		return true, ""
	}
	plain, err := dec.Decode(blob)
	if err != nil {
		return false, "could not decode blob"
	}
	if !store.VerifyDigest(chunk.Digest, plain) {
		return false, "content does not match its digest"
	}
	return true, ""
}

func (r *Runner) reconcileAlerts(ctx context.Context) error {
	rows, err := r.db.SQL().QueryContext(ctx, `SELECT id FROM users`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := r.db.ReconcileDeviceAlerts(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
