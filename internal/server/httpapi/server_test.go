package httpapi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/codec"
	"github.com/foisalislambd/openbackup/internal/hash"
	"github.com/foisalislambd/openbackup/internal/server/config"
	"github.com/foisalislambd/openbackup/internal/server/httpapi"
	"github.com/foisalislambd/openbackup/internal/server/maintenance"
	"github.com/foisalislambd/openbackup/internal/server/store"
)

// harness is a running server plus the two clients that talk to it: a browser
// session and an enrolled agent.
type harness struct {
	t      *testing.T
	server *httptest.Server
	db     *store.DB
	blobs  store.Blobs
	ui     *http.Client
	codec  *codec.Codec
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()

	db, err := store.OpenDB(ctx, dir+"/test.db")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	blobs, err := store.NewFSBlobs(dir + "/blobs")
	if err != nil {
		t.Fatalf("NewFSBlobs: %v", err)
	}

	cfg := config.Default()
	cfg.DataDir = dir
	cfg.RetentionDays = 30
	srv, err := httpapi.New(httpapi.Options{
		Config: cfg,
		DB:     db,
		Blobs:  blobs,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("httpapi.New: %v", err)
	}
	t.Cleanup(srv.Close)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := codec.New(codec.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)

	return &harness{
		t:      t,
		server: ts,
		db:     db,
		blobs:  blobs,
		ui:     &http.Client{Jar: jar, Timeout: 30 * time.Second},
		codec:  c,
	}
}

// do performs a dashboard request as the signed-in browser.
func (h *harness) do(method, path string, body any, out any) *http.Response {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.ui.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	if out != nil {
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			h.t.Fatalf("decode %s %s: %v", method, path, err)
		}
	}
	return resp
}

// setupAccount creates the first admin and signs the browser in.
func (h *harness) setupAccount() {
	h.t.Helper()
	resp := h.do(http.MethodPost, "/api/v1/ui/setup",
		map[string]string{"email": "owner@example.com", "password": "a-long-enough-password"}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		h.t.Fatalf("setup returned %d", resp.StatusCode)
	}
}

// enroll creates a join code and exchanges it for an agent client.
func (h *harness) enroll(name string) *api.Client {
	h.t.Helper()
	var invite struct {
		Code string `json:"code"`
	}
	resp := h.do(http.MethodPost, "/api/v1/ui/join-tokens", map[string]string{"label": name}, &invite)
	if resp.StatusCode != http.StatusCreated {
		h.t.Fatalf("join-tokens returned %d", resp.StatusCode)
	}

	client, err := api.NewClient(h.server.URL, "")
	if err != nil {
		h.t.Fatal(err)
	}
	client.MaxRetries = 1
	if _, err := client.Enroll(context.Background(), api.EnrollRequest{
		JoinToken:    invite.Code,
		DeviceName:   name,
		Hostname:     name,
		Platform:     api.PlatformLinux,
		AgentVersion: "test",
	}); err != nil {
		h.t.Fatalf("Enroll: %v", err)
	}
	return client
}

// backup uploads files as one snapshot and returns its id.
func (h *harness) backup(client *api.Client, kind api.SnapshotKind, parent string, files map[string][]byte, deleted []string) string {
	h.t.Helper()
	ctx := context.Background()

	id, err := client.StartSnapshot(ctx, api.StartSnapshotRequest{
		Roots:     []api.SnapshotRoot{{Name: "documents", Path: "/home/test/Documents"}},
		Kind:      kind,
		ParentID:  parent,
		StartedAt: time.Now().UTC(),
	})
	if err != nil {
		h.t.Fatalf("StartSnapshot: %v", err)
	}

	var entries []api.Entry
	var total int64
	for path, content := range files {
		digest := hash.Sum(content)
		missing, err := client.MissingChunks(ctx, []string{digest})
		if err != nil {
			h.t.Fatalf("MissingChunks: %v", err)
		}
		for _, d := range missing {
			blob, err := h.codec.Encode(content, d)
			if err != nil {
				h.t.Fatal(err)
			}
			if err := client.PutChunk(ctx, d, blob, len(content)); err != nil {
				h.t.Fatalf("PutChunk: %v", err)
			}
		}
		entries = append(entries, api.Entry{
			Path:    path,
			Type:    api.EntryFile,
			Size:    int64(len(content)),
			ModTime: time.Now().UTC().Truncate(time.Second),
			Chunks:  []string{digest},
			Digest:  digest,
		})
		total += int64(len(content))
	}
	if err := client.AddEntries(ctx, id, api.AddEntriesRequest{Entries: entries, Deleted: deleted}); err != nil {
		h.t.Fatalf("AddEntries: %v", err)
	}
	if err := client.CompleteSnapshot(ctx, id, api.CompleteSnapshotRequest{
		CompletedAt: time.Now().UTC(),
		FileCount:   int64(len(files)),
		TotalBytes:  total,
	}); err != nil {
		h.t.Fatalf("CompleteSnapshot: %v", err)
	}
	return id
}

func TestFullBackupAndRestoreFlow(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")

	files := map[string][]byte{
		"Documents/notes.txt":   []byte("remember to test restores"),
		"Documents/report.docx": bytes.Repeat([]byte("content "), 500),
		"Pictures/holiday.jpg":  bytes.Repeat([]byte{0x42}, 4096),
	}
	snapshotID := h.backup(client, api.SnapshotFull, "", files, nil)

	var listed struct {
		Snapshots []api.Snapshot `json:"snapshots"`
	}
	resp := h.do(http.MethodGet, "/api/v1/ui/snapshots", nil, &listed)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list snapshots returned %d", resp.StatusCode)
	}
	if len(listed.Snapshots) != 1 || listed.Snapshots[0].ID != snapshotID {
		t.Fatalf("expected one snapshot %s, got %+v", snapshotID, listed.Snapshots)
	}
	if listed.Snapshots[0].Status != api.SnapshotStatusComplete {
		t.Fatalf("snapshot status = %q, want complete", listed.Snapshots[0].Status)
	}

	// Browsing must show every file that was backed up.
	var browsed struct {
		Entries []api.Entry `json:"entries"`
	}
	h.do(http.MethodGet, "/api/v1/ui/snapshots/"+snapshotID+"/browse", nil, &browsed)
	if len(browsed.Entries) != len(files) {
		t.Fatalf("browse returned %d entries, want %d", len(browsed.Entries), len(files))
	}

	// Every file must come back byte for byte.
	for path, want := range files {
		resp := h.do(http.MethodGet,
			"/api/v1/ui/snapshots/"+snapshotID+"/download?path="+url.QueryEscape(path), nil, nil)
		got, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("download %s returned %d: %s", path, resp.StatusCode, got)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("restored %s does not match the original (%d vs %d bytes)", path, len(got), len(want))
		}
	}
}

func TestArchiveRestoreContainsEveryFile(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")

	files := map[string][]byte{
		"Documents/a.txt":     []byte("alpha"),
		"Documents/sub/b.txt": []byte("beta"),
		"Pictures/c.jpg":      []byte("gamma"),
	}
	snapshotID := h.backup(client, api.SnapshotFull, "", files, nil)

	resp := h.do(http.MethodGet, "/api/v1/ui/snapshots/"+snapshotID+"/archive?prefix=Documents", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("archive returned %d: %s", resp.StatusCode, body)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}

	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		got[f.Name] = string(content)
	}
	// Paths are relative to the requested prefix, and files outside it are not
	// included.
	if got["a.txt"] != "alpha" || got["sub/b.txt"] != "beta" {
		t.Fatalf("archive contents unexpected: %v", got)
	}
	if _, leaked := got["Pictures/c.jpg"]; leaked {
		t.Fatal("archive included a file outside the requested prefix")
	}
}

// Delta snapshots are the whole point of the incremental design: this checks
// that a change, an addition and a deletion all resolve correctly against the
// parent snapshot.
func TestDeltaSnapshotResolvesAgainstParent(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")

	full := h.backup(client, api.SnapshotFull, "", map[string][]byte{
		"Documents/keep.txt":   []byte("unchanged"),
		"Documents/change.txt": []byte("version one"),
		"Documents/delete.txt": []byte("temporary"),
	}, nil)

	delta := h.backup(client, api.SnapshotDelta, full, map[string][]byte{
		"Documents/change.txt": []byte("version two"),
		"Documents/new.txt":    []byte("brand new"),
	}, []string{"Documents/delete.txt"})

	entries, _, err := h.db.Tree(context.Background(), delta, store.TreeQuery{Limit: 100})
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	paths := map[string]int64{}
	for _, e := range entries {
		paths[e.Path] = e.Size
	}
	if _, ok := paths["Documents/keep.txt"]; !ok {
		t.Error("unchanged file should be inherited from the parent snapshot")
	}
	if _, ok := paths["Documents/new.txt"]; !ok {
		t.Error("new file should appear in the delta")
	}
	if _, ok := paths["Documents/delete.txt"]; ok {
		t.Error("deleted file should not appear in the delta's tree")
	}
	if paths["Documents/change.txt"] != int64(len("version two")) {
		t.Errorf("changed file has size %d, want the newer version", paths["Documents/change.txt"])
	}

	// The parent snapshot must still show the world as it was.
	parentEntries, _, err := h.db.Tree(context.Background(), full, store.TreeQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var foundDeleted bool
	for _, e := range parentEntries {
		if e.Path == "Documents/delete.txt" {
			foundDeleted = true
		}
	}
	if !foundDeleted {
		t.Error("history must be preserved: the older snapshot should still contain the deleted file")
	}
}

// Deduplication is the headline efficiency claim, so it gets a test: identical
// content from a second device must not be uploaded again.
func TestDeduplicationAcrossDevices(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	laptop := h.enroll("laptop")
	phone := h.enroll("phone")

	content := bytes.Repeat([]byte("family photo bytes"), 1000)
	digest := hash.Sum(content)

	h.backup(laptop, api.SnapshotFull, "", map[string][]byte{"Pictures/photo.jpg": content}, nil)

	missing, err := phone.MissingChunks(context.Background(), []string{digest})
	if err != nil {
		t.Fatalf("MissingChunks: %v", err)
	}
	if len(missing) != 0 {
		t.Fatal("a chunk already uploaded by another device must not be requested again")
	}

	h.backup(phone, api.SnapshotFull, "", map[string][]byte{"DCIM/photo.jpg": content}, nil)

	var usage api.UsageStats
	h.do(http.MethodGet, "/api/v1/ui/usage", nil, &usage)
	if usage.ChunkCount != 1 {
		t.Fatalf("expected the shared content to be stored once, got %d chunks", usage.ChunkCount)
	}
	if usage.DeviceCount != 2 {
		t.Fatalf("expected 2 devices, got %d", usage.DeviceCount)
	}
	if usage.DedupRatio <= 1 {
		t.Fatalf("expected a dedup ratio above 1, got %v", usage.DedupRatio)
	}
}

func TestChunkUploadIsIdempotentAndVerified(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")
	ctx := context.Background()

	content := []byte("verify me")
	digest := hash.Sum(content)
	blob, err := h.codec.Encode(content, digest)
	if err != nil {
		t.Fatal(err)
	}

	// Uploading twice must succeed: agents retry after timeouts.
	for i := range 2 {
		if err := client.PutChunk(ctx, digest, blob, len(content)); err != nil {
			t.Fatalf("PutChunk attempt %d: %v", i+1, err)
		}
	}

	// A blob whose content does not match its digest must be rejected outright,
	// or the repository would silently corrupt.
	wrong, err := h.codec.Encode([]byte("different content"), hash.Sum([]byte("different content")))
	if err != nil {
		t.Fatal(err)
	}
	err = client.PutChunk(ctx, hash.Sum([]byte("something else entirely")), wrong, 17)
	if err == nil {
		t.Fatal("expected a chunk that does not match its digest to be rejected")
	}
}

func TestSnapshotWithMissingChunkCannotComplete(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")
	ctx := context.Background()

	id, err := client.StartSnapshot(ctx, api.StartSnapshotRequest{Kind: api.SnapshotFull, StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	// Reference a chunk that was never uploaded.
	phantom := hash.Sum([]byte("never uploaded"))
	if err := client.AddEntries(ctx, id, api.AddEntriesRequest{Entries: []api.Entry{{
		Path: "Documents/ghost.txt", Type: api.EntryFile, Size: 14, Chunks: []string{phantom},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := client.CompleteSnapshot(ctx, id, api.CompleteSnapshotRequest{FileCount: 1}); err == nil {
		t.Fatal("a snapshot referencing missing data must not be marked complete")
	}

	snap, err := h.db.SnapshotByID(ctx, snapshotOwner(t, h, id), id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != api.SnapshotStatusFailed {
		t.Fatalf("snapshot status = %q, want failed", snap.Status)
	}
}

// randomBytes returns incompressible test data.
func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}

// snapshotOwner finds the account id owning a snapshot, for assertions that go
// straight to the database.
func snapshotOwner(t *testing.T, h *harness, snapshotID string) string {
	t.Helper()
	user, err := h.db.FirstUser(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return user.ID
}

func TestDeletingSnapshotFreesStorage(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")
	ctx := context.Background()

	content := bytes.Repeat([]byte("delete me"), 500)
	digest := hash.Sum(content)
	first := h.backup(client, api.SnapshotFull, "", map[string][]byte{"Documents/a.txt": content}, nil)
	// A second snapshot keeps the device from being left with nothing, which
	// retention would otherwise protect.
	h.backup(client, api.SnapshotFull, "", map[string][]byte{"Documents/b.txt": []byte("other")}, nil)

	if _, ok := h.blobs.Has(ctx, digest); !ok {
		t.Fatal("expected the chunk to be stored")
	}

	resp := h.do(http.MethodDelete, "/api/v1/ui/snapshots/"+first, nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete snapshot returned %d", resp.StatusCode)
	}

	report, err := maintenance.New(h.db, h.blobs, slog.New(slog.DiscardHandler)).Run(ctx)
	if err != nil {
		t.Fatalf("maintenance: %v", err)
	}
	if report.DeletedChunks == 0 {
		t.Fatal("garbage collection should have removed the now-unreferenced chunk")
	}
	if _, ok := h.blobs.Has(ctx, digest); ok {
		t.Fatal("the unreferenced blob should be gone from storage")
	}
}

// Retention must never leave a device with no restorable backup, however old its
// last one is.
func TestRetentionKeepsTheNewestSnapshot(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")
	ctx := context.Background()

	id := h.backup(client, api.SnapshotFull, "", map[string][]byte{"Documents/old.txt": []byte("ancient")}, nil)
	// Backdate it well past any retention window.
	if _, err := h.db.SQL().ExecContext(ctx, `UPDATE snapshots SET started_at = ?, completed_at = ? WHERE id = ?`,
		time.Now().AddDate(-2, 0, 0).UnixMilli(), time.Now().AddDate(-2, 0, 0).UnixMilli(), id); err != nil {
		t.Fatal(err)
	}

	if _, err := maintenance.New(h.db, h.blobs, slog.New(slog.DiscardHandler)).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.SnapshotByID(ctx, snapshotOwner(t, h, id), id); err != nil {
		t.Fatalf("the only snapshot of a device must survive retention: %v", err)
	}
}

func TestAuthenticationIsRequired(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()

	// A fresh client with no cookie jar stands in for an unauthenticated browser.
	anon := &http.Client{}
	for _, path := range []string{"/api/v1/ui/devices", "/api/v1/ui/usage", "/api/v1/ui/snapshots"} {
		resp, err := anon.Get(h.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without a session returned %d, want 401", path, resp.StatusCode)
		}
	}

	// And an agent endpoint without a device token.
	resp, err := anon.Post(h.server.URL+api.PathChunksMissing, "application/json", strings.NewReader(`{"digests":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("agent endpoint without a token returned %d, want 401", resp.StatusCode)
	}
}

func TestOneDeviceCannotTouchAnothersSnapshot(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	laptop := h.enroll("laptop")
	phone := h.enroll("phone")
	ctx := context.Background()

	id, err := laptop.StartSnapshot(ctx, api.StartSnapshotRequest{Kind: api.SnapshotFull, StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	err = phone.AddEntries(ctx, id, api.AddEntriesRequest{Entries: []api.Entry{{Path: "x", Type: api.EntryFile}}})
	if err == nil {
		t.Fatal("a device must not be able to write into another device's snapshot")
	}
}

func TestJoinCodeIsSingleUse(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()

	var invite struct {
		Code string `json:"code"`
	}
	h.do(http.MethodPost, "/api/v1/ui/join-tokens", nil, &invite)

	enrollOnce := func() error {
		client, err := api.NewClient(h.server.URL, "")
		if err != nil {
			return err
		}
		client.MaxRetries = 0
		_, err = client.Enroll(context.Background(), api.EnrollRequest{
			JoinToken: invite.Code, DeviceName: "d", Platform: api.PlatformLinux, AgentVersion: "test",
		})
		return err
	}
	if err := enrollOnce(); err != nil {
		t.Fatalf("first enrolment: %v", err)
	}
	if err := enrollOnce(); err == nil {
		t.Fatal("a join code must not work twice")
	}
}

func TestRevokedDeviceCannotUpload(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")
	ctx := context.Background()

	var devices struct {
		Devices []api.Device `json:"devices"`
	}
	h.do(http.MethodGet, "/api/v1/ui/devices", nil, &devices)
	if len(devices.Devices) != 1 {
		t.Fatalf("expected one device, got %d", len(devices.Devices))
	}
	resp := h.do(http.MethodDelete, "/api/v1/ui/devices/"+devices.Devices[0].ID, nil, nil)
	resp.Body.Close()

	_, err := client.StartSnapshot(ctx, api.StartSnapshotRequest{Kind: api.SnapshotFull, StartedAt: time.Now()})
	if err == nil {
		t.Fatal("a revoked device must not be able to start a backup")
	}
	if !api.IsAuthError(err) {
		t.Fatalf("expected an auth error so the agent stops retrying, got %v", err)
	}
}

func TestIgnoreRulesArePublished(t *testing.T) {
	h := newHarness(t)
	resp, err := http.Get(h.server.URL + "/api/v1/ui/ignore-rules")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Categories map[string][]struct {
			Pattern string `json:"pattern"`
			Reason  string `json:"reason"`
		} `json:"categories"`
		ProjectMarkers []string `json:"project_markers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Categories["developer"]) == 0 || len(out.Categories["system"]) == 0 {
		t.Fatal("expected published developer and system rules")
	}
	for category, rules := range out.Categories {
		for _, rule := range rules {
			if rule.Reason == "" {
				t.Errorf("rule %q in %s has no explanation for the user", rule.Pattern, category)
			}
		}
	}
	if len(out.ProjectMarkers) == 0 {
		t.Fatal("expected the project marker list to be published")
	}
}

func TestHealthEndpoint(t *testing.T) {
	h := newHarness(t)
	resp, err := http.Get(h.server.URL + api.PathHealth)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health returned %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["protocol"] != api.Version {
		t.Fatalf("health reported protocol %v, want %s", out["protocol"], api.Version)
	}
}

func TestSetupOnlyWorksOnce(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	resp := h.do(http.MethodPost, "/api/v1/ui/setup",
		map[string]string{"email": "attacker@example.com", "password": "another-long-password"}, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second setup returned %d, want 409", resp.StatusCode)
	}
}

func TestLoginFlow(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()

	resp := h.do(http.MethodPost, "/api/v1/ui/logout", nil, nil)
	resp.Body.Close()

	resp = h.do(http.MethodGet, "/api/v1/ui/me", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout /me returned %d, want 401", resp.StatusCode)
	}

	resp = h.do(http.MethodPost, "/api/v1/ui/login",
		map[string]string{"email": "owner@example.com", "password": "wrong-password-here"}, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad password returned %d, want 401", resp.StatusCode)
	}

	resp = h.do(http.MethodPost, "/api/v1/ui/login",
		map[string]string{"email": "OWNER@example.com", "password": "a-long-enough-password"}, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login returned %d; email comparison should be case-insensitive", resp.StatusCode)
	}
	resp = h.do(http.MethodGet, "/api/v1/ui/me", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/me after login returned %d", resp.StatusCode)
	}
}

func TestHeartbeatDeliversQueuedCommands(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")
	ctx := context.Background()

	var devices struct {
		Devices []api.Device `json:"devices"`
	}
	h.do(http.MethodGet, "/api/v1/ui/devices", nil, &devices)
	deviceID := devices.Devices[0].ID

	resp := h.do(http.MethodPost, "/api/v1/ui/devices/"+deviceID+"/commands",
		map[string]string{"kind": string(api.CommandBackupNow)}, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("queue command returned %d", resp.StatusCode)
	}

	hb, err := client.Heartbeat(ctx, api.HeartbeatRequest{State: api.StateIdle, AgentVersion: "test"})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if len(hb.Commands) != 1 || hb.Commands[0].Kind != api.CommandBackupNow {
		t.Fatalf("expected one backup_now command, got %+v", hb.Commands)
	}
	if hb.Policy.ChunkAvgSize == 0 {
		t.Fatal("heartbeat must return the chunking policy so agents stay consistent")
	}

	// Commands are drained, not replayed.
	hb, err = client.Heartbeat(ctx, api.HeartbeatRequest{State: api.StateIdle})
	if err != nil {
		t.Fatal(err)
	}
	if len(hb.Commands) != 0 {
		t.Fatalf("commands should be delivered once, got %+v", hb.Commands)
	}
}

func TestEncryptedRepositoryRefusesServerSideRestore(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")
	ctx := context.Background()

	key, err := codec.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := codec.New(codec.Options{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	defer sealed.Close()

	content := []byte("private diary")
	digest := hash.Sum(content)
	blob, err := sealed.Encode(content, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.PutChunk(ctx, digest, blob, len(content)); err != nil {
		t.Fatalf("PutChunk: %v", err)
	}

	id, err := client.StartSnapshot(ctx, api.StartSnapshotRequest{Kind: api.SnapshotFull, StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AddEntries(ctx, id, api.AddEntriesRequest{Entries: []api.Entry{{
		Path: "Documents/diary.txt", Type: api.EntryFile, Size: int64(len(content)),
		Chunks: []string{digest}, Digest: digest,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := client.CompleteSnapshot(ctx, id, api.CompleteSnapshotRequest{FileCount: 1}); err != nil {
		t.Fatal(err)
	}

	// The server holds no key, so it must say so clearly instead of serving
	// ciphertext or a generic error.
	resp := h.do(http.MethodGet,
		"/api/v1/ui/snapshots/"+id+"/download?path="+url.QueryEscape("Documents/diary.txt"), nil, nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("download of an encrypted file returned %d, want 409: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "end-to-end encrypted") {
		t.Fatalf("expected an explanation mentioning encryption, got %s", body)
	}
}

// A restore browser walks one folder at a time, so a listing must not spill the
// whole subtree into the current level: that made every nested file look like it
// lived at the root.
func TestBrowsingListsOneFolderAtATime(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")

	id := h.backup(client, api.SnapshotFull, "", map[string][]byte{
		"home/Documents/report.txt":  []byte("report"),
		"home/Documents/tax/2026.md": []byte("taxes"),
		"home/Pictures/holiday.jpg":  []byte("photo"),
	}, nil)

	list := func(prefix string, directOnly bool) []string {
		entries, _, err := h.db.Tree(context.Background(), id,
			store.TreeQuery{Prefix: prefix, Limit: 100, DirectOnly: directOnly})
		if err != nil {
			t.Fatalf("Tree(%q): %v", prefix, err)
		}
		paths := make([]string, 0, len(entries))
		for _, e := range entries {
			paths = append(paths, e.Path)
		}
		return paths
	}

	if got := list("", true); !slices.Equal(got, []string{"home"}) {
		t.Errorf("root listing = %v, want just the backed-up root", got)
	}
	if got := list("home/Documents", true); !slices.Equal(got,
		[]string{"home/Documents/report.txt", "home/Documents/tax"}) {
		t.Errorf("folder listing = %v, want its direct children only", got)
	}

	// A restore of the same folder still needs everything underneath it.
	if got := list("home/Documents", false); !slices.Equal(got,
		[]string{"home/Documents/report.txt", "home/Documents/tax/2026.md"}) {
		t.Errorf("subtree listing = %v, want every file below the folder", got)
	}
}

func TestQuotaIsEnforced(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")
	ctx := context.Background()

	// A tiny quota, then two uploads: the first fills it, the second is refused.
	// The payloads are random because quotas count *stored* bytes, and
	// compressible test data would slip under the limit.
	resp := h.do(http.MethodPut, "/api/v1/ui/settings", map[string]any{"quota_bytes": 1024}, nil)
	resp.Body.Close()

	first := randomBytes(t, 4096)
	firstDigest := hash.Sum(first)
	blob, err := h.codec.Encode(first, firstDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.PutChunk(ctx, firstDigest, blob, len(first)); err != nil {
		t.Fatalf("the first upload should be allowed: %v", err)
	}
	// The snapshot reference is what makes the stored bytes count against usage.
	id := h.backup(client, api.SnapshotFull, "", map[string][]byte{"Documents/a.txt": first}, nil)
	if id == "" {
		t.Fatal("expected a snapshot")
	}

	second := randomBytes(t, 4096)
	secondDigest := hash.Sum(second)
	blob2, err := h.codec.Encode(second, secondDigest)
	if err != nil {
		t.Fatal(err)
	}
	err = client.PutChunk(ctx, secondDigest, blob2, len(second))
	if err == nil {
		t.Fatal("expected the upload to be refused once the quota was full")
	}
	if !api.IsQuotaError(err) {
		t.Fatalf("expected a quota error the agent can report to the user, got %v", err)
	}
}

func TestEventsFeed(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")
	ctx := context.Background()

	err := client.SendEvents(ctx, []api.Event{
		{At: time.Now().UTC(), Level: "info", Message: "backup started"},
		{At: time.Now().UTC(), Level: "warn", Message: "skipped a folder",
			Path: "Code/api/node_modules", Reason: "reinstallable with npm install"},
	})
	if err != nil {
		t.Fatalf("SendEvents: %v", err)
	}

	var out struct {
		Events []store.StoredEvent `json:"events"`
	}
	h.do(http.MethodGet, "/api/v1/ui/events", nil, &out)
	if len(out.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(out.Events))
	}
	var foundReason bool
	for _, e := range out.Events {
		if e.Reason != "" {
			foundReason = true
		}
		if e.DeviceName != "laptop" {
			t.Errorf("event should be attributed to the device, got %q", e.DeviceName)
		}
	}
	if !foundReason {
		t.Error("skip reasons must reach the dashboard so users can see why a folder was excluded")
	}
}

func TestBrowsePaginates(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")

	files := map[string][]byte{}
	for i := range 25 {
		files[fmt.Sprintf("Documents/file-%02d.txt", i)] = []byte(fmt.Sprintf("content %d", i))
	}
	id := h.backup(client, api.SnapshotFull, "", files, nil)

	seen := map[string]bool{}
	cursor := ""
	for range 10 {
		var page struct {
			Entries    []api.Entry `json:"entries"`
			NextCursor string      `json:"next_cursor"`
		}
		h.do(http.MethodGet, "/api/v1/ui/snapshots/"+id+"/browse?limit=10&cursor="+url.QueryEscape(cursor), nil, &page)
		for _, e := range page.Entries {
			if seen[e.Path] {
				t.Fatalf("entry %s returned twice while paginating", e.Path)
			}
			seen[e.Path] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != len(files) {
		t.Fatalf("pagination returned %d of %d entries", len(seen), len(files))
	}
}

// TestInstallScriptIsSelfContained guards the one-command install. The script is
// served unauthenticated on purpose (it contains no secret, and the device still
// needs a one-time code), but it must name this server, or the user ends up with
// an agent pointing nowhere.
func TestInstallScriptIsSelfContained(t *testing.T) {
	h := newHarness(t)

	resp, err := http.Get(h.server.URL + "/install.sh")
	if err != nil {
		t.Fatalf("GET /install.sh: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /install.sh returned %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)

	if !strings.HasPrefix(script, "#!/bin/sh") {
		t.Error("the installer must start with a shebang so 'curl | sh' works")
	}
	if strings.Contains(script, "__SERVER_URL__") || strings.Contains(script, "__VERSION__") {
		t.Error("placeholders were not substituted, so the printed instructions are wrong")
	}
	if !strings.Contains(script, h.server.Listener.Addr().String()) {
		t.Errorf("the installer does not mention this server's address:\n%s", script)
	}
	if !strings.Contains(script, "openbackup connect") {
		t.Error("the installer must tell the user how to connect the device")
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "shellscript") {
		t.Errorf("Content-Type = %q, want a shell script type", ct)
	}
}

// TestBootstrapReportsAuthState covers the one call the dashboard makes before it
// renders anything. Getting it wrong shows a signed-in user the login form, or
// worse, shows the dashboard to someone who is not signed in.
func TestBootstrapReportsAuthState(t *testing.T) {
	h := newHarness(t)

	type bootstrap struct {
		NeedsSetup    bool   `json:"needs_setup"`
		Authenticated bool   `json:"authenticated"`
		Version       string `json:"version"`
	}

	var fresh bootstrap
	h.do(http.MethodGet, "/api/v1/ui/bootstrap", nil, &fresh)
	if !fresh.NeedsSetup || fresh.Authenticated {
		t.Fatalf("a fresh server must ask for setup and report nobody signed in, got %+v", fresh)
	}

	h.setupAccount()

	var ready bootstrap
	h.do(http.MethodGet, "/api/v1/ui/bootstrap", nil, &ready)
	if ready.NeedsSetup {
		t.Error("setup must not be requested twice")
	}
	if !ready.Authenticated {
		t.Error("creating the first account must sign that browser in")
	}

	resp := h.do(http.MethodPost, "/api/v1/ui/logout", nil, nil)
	resp.Body.Close()

	var after bootstrap
	h.do(http.MethodGet, "/api/v1/ui/bootstrap", nil, &after)
	if after.Authenticated {
		t.Error("bootstrap still reports a session after signing out")
	}
}

// TestSettingsReachAgents is the whole point of the settings page: a change made
// in the browser must arrive at every device without anyone touching them.
func TestSettingsReachAgents(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("laptop")

	type settings struct {
		RetentionDays        int   `json:"retention_days"`
		QuotaBytes           int64 `json:"quota_bytes"`
		MaxUploadBytesPerSec int64 `json:"max_upload_bytes_per_sec"`
		RequireEncryption    bool  `json:"require_encryption"`
	}

	var saved settings
	resp := h.do(http.MethodPut, "/api/v1/ui/settings", map[string]any{
		"retention_days":           90,
		"max_upload_bytes_per_sec": 2 << 20,
	}, &saved)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT settings returned %d", resp.StatusCode)
	}
	if saved.RetentionDays != 90 || saved.MaxUploadBytesPerSec != 2<<20 {
		t.Fatalf("settings were not stored: %+v", saved)
	}

	// A partial update must not reset the fields it does not mention.
	var second settings
	h.do(http.MethodPut, "/api/v1/ui/settings", map[string]any{"quota_bytes": 5 << 30}, &second)
	if second.RetentionDays != 90 || second.MaxUploadBytesPerSec != 2<<20 || second.QuotaBytes != 5<<30 {
		t.Fatalf("a partial update clobbered other settings: %+v", second)
	}

	hb, err := client.Heartbeat(context.Background(), api.HeartbeatRequest{State: api.StateIdle})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if hb.Policy.RetentionDays != 90 {
		t.Errorf("agent policy retention = %d, want 90", hb.Policy.RetentionDays)
	}
	if hb.Policy.MaxUploadBytesPerSec != 2<<20 {
		t.Errorf("agent policy upload limit = %d, want %d", hb.Policy.MaxUploadBytesPerSec, 2<<20)
	}
	if hb.Policy.QuotaBytes != 5<<30 {
		t.Errorf("agent policy quota = %d, want %d", hb.Policy.QuotaBytes, 5<<30)
	}
}

// TestRequireEncryptionRejectsPlaintext checks that the setting is enforced where
// it matters, at the moment a chunk is stored, rather than being advice.
func TestRequireEncryptionRejectsPlaintext(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()

	var saved struct {
		RequireEncryption bool `json:"require_encryption"`
	}
	resp := h.do(http.MethodPut, "/api/v1/ui/settings", map[string]any{"require_encryption": true}, &saved)
	if resp.StatusCode != http.StatusOK || !saved.RequireEncryption {
		t.Fatalf("could not require encryption: status %d, %+v", resp.StatusCode, saved)
	}

	client := h.enroll("laptop")
	content := []byte("a plaintext file that must be refused")
	digest := hash.Sum(content)
	blob, err := h.codec.Encode(content, digest)
	if err != nil {
		t.Fatal(err)
	}
	err = client.PutChunk(context.Background(), digest, blob, len(content))
	if err == nil {
		t.Fatal("server accepted a plaintext chunk while the account requires encryption")
	}

	// Once backups exist, turning the requirement on would misrepresent what is
	// already stored, so the server must refuse rather than mislead.
	h2 := newHarness(t)
	h2.setupAccount()
	other := h2.enroll("desktop")
	h2.backup(other, api.SnapshotFull, "", map[string][]byte{"Documents/a.txt": []byte("hello")}, nil)
	resp = h2.do(http.MethodPut, "/api/v1/ui/settings", map[string]any{"require_encryption": true}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("enabling encryption after a backup returned %d, want 409", resp.StatusCode)
	}
}
