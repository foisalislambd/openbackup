package engine_test

import (
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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/openbackup/openbackup/internal/agent/config"
	"github.com/openbackup/openbackup/internal/agent/engine"
	"github.com/openbackup/openbackup/internal/agent/restore"
	"github.com/openbackup/openbackup/internal/api"
	"github.com/openbackup/openbackup/internal/codec"
	srvconfig "github.com/openbackup/openbackup/internal/server/config"
	"github.com/openbackup/openbackup/internal/server/httpapi"
	"github.com/openbackup/openbackup/internal/server/store"
)

// fixture is a real server, a real agent configuration, and a folder of files to
// back up. These tests exercise the whole product end to end, because that is
// the only way to know a backup tool works: unit tests cannot tell you that a
// file came back byte for byte.
type fixture struct {
	t        *testing.T
	server   *httptest.Server
	db       *store.DB
	home     string
	stateDir string
	cfg      *config.Config
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()

	db, err := store.OpenDB(ctx, filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	blobs, err := store.NewFSBlobs(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	scfg := srvconfig.Default()
	scfg.DataDir = dir
	srv, err := httpapi.New(httpapi.Options{
		Config: scfg, DB: db, Blobs: blobs, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	f := &fixture{
		t:        t,
		server:   ts,
		db:       db,
		home:     filepath.Join(dir, "home"),
		stateDir: filepath.Join(dir, "state"),
	}
	if err := os.MkdirAll(f.home, 0o755); err != nil {
		t.Fatal(err)
	}

	// Set up the account and enrol the agent through the real HTTP flow.
	jar, _ := cookiejar.New(nil)
	ui := &http.Client{Jar: jar}
	post := func(path string, body any, out any) {
		raw, _ := json.Marshal(body)
		resp, err := ui.Post(ts.URL+path, "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			msg, _ := io.ReadAll(resp.Body)
			t.Fatalf("POST %s returned %d: %s", path, resp.StatusCode, msg)
		}
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
	}
	post("/api/v1/ui/setup", map[string]string{
		"email": "owner@example.com", "password": "a-long-enough-password"}, nil)
	var invite struct {
		Code string `json:"code"`
	}
	post("/api/v1/ui/join-tokens", map[string]string{"label": "test"}, &invite)

	client, err := api.NewClient(ts.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := client.Enroll(ctx, api.EnrollRequest{
		JoinToken: invite.Code, DeviceName: "test-device", Hostname: "test-device",
		Platform: api.PlatformLinux, AgentVersion: "test",
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}

	cfg := config.Default()
	cfg.ServerURL = ts.URL
	cfg.DeviceID = enrolled.DeviceID
	cfg.DeviceToken = enrolled.DeviceToken
	cfg.DeviceName = "test-device"
	cfg.Roots = []config.Root{{Name: "home", Path: f.home, Enabled: true}}
	// Nothing in a test should be paused by a busy CI machine or a laptop on
	// battery, so the governor is taken out of the way deliberately.
	cfg.Limits.MaxCPUPercent = 0
	cfg.Limits.PauseOnMetered = false
	cfg.Limits.PauseWhileFullscreen = false
	f.cfg = cfg
	return f
}

// write creates a file under the backed-up home directory.
func (f *fixture) write(rel, content string) {
	f.t.Helper()
	path := filepath.Join(f.home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// backup runs one full backup with a fresh engine, as the CLI's 'backup' does.
func (f *fixture) backup() {
	f.t.Helper()
	ctx := context.Background()
	eng, err := engine.New(ctx, engine.Options{
		Config: f.cfg, Logger: slog.New(slog.DiscardHandler), StateDir: f.stateDir,
	})
	if err != nil {
		f.t.Fatalf("engine.New: %v", err)
	}
	defer eng.Close()
	if err := eng.RunOnce(ctx); err != nil {
		f.t.Fatalf("backup: %v", err)
	}
}

// latestTree returns the resolved file list of the newest snapshot.
func (f *fixture) latestTree() map[string]api.Entry {
	f.t.Helper()
	ctx := context.Background()
	client, err := api.NewClient(f.cfg.ServerURL, f.cfg.DeviceToken)
	if err != nil {
		f.t.Fatal(err)
	}
	snap, err := restore.FindSnapshot(ctx, client, "latest")
	if err != nil {
		f.t.Fatalf("find snapshot: %v", err)
	}
	out := map[string]api.Entry{}
	cursor := ""
	for {
		entries, next, err := client.SnapshotEntries(ctx, snap.ID, "", cursor, 500)
		if err != nil {
			f.t.Fatal(err)
		}
		for _, e := range entries {
			out[e.Path] = e
		}
		if next == "" {
			return out
		}
		cursor = next
	}
}

func TestAgentBacksUpAndRestoresFiles(t *testing.T) {
	f := newFixture(t)
	f.write("Documents/notes.txt", "the quick brown fox")
	f.write("Documents/work/report.md", strings.Repeat("content\n", 2000))
	f.write("Pictures/photo.jpg", "not really a jpeg")

	f.backup()

	tree := f.latestTree()
	for _, want := range []string{"home/Documents/notes.txt", "home/Documents/work/report.md", "home/Pictures/photo.jpg"} {
		if _, ok := tree[want]; !ok {
			t.Errorf("expected %s in the backup; got %v", want, keys(tree))
		}
	}

	// Restore into a clean directory and compare byte for byte.
	target := filepath.Join(t.TempDir(), "restored")
	ctx := context.Background()
	client, _ := api.NewClient(f.cfg.ServerURL, f.cfg.DeviceToken)
	c, err := codec.New(codec.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	snap, err := restore.FindSnapshot(ctx, client, "latest")
	if err != nil {
		t.Fatal(err)
	}
	result, err := restore.Run(ctx, restore.Options{
		Client: client, Codec: c, SnapshotID: snap.ID, Target: target,
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(result.Failed) > 0 {
		t.Fatalf("restore reported failures: %v", result.Failed)
	}

	compareTrees(t, f.home, filepath.Join(target, "home"))
}

// The ignore rules are the feature most likely to do damage if they are wrong,
// in either direction: excluding a user's work, or dragging in gigabytes of
// reinstallable junk.
func TestAgentSkipsJunkButKeepsSourceCode(t *testing.T) {
	f := newFixture(t)

	// A real-looking Node project.
	f.write("Code/app/package.json", `{"name":"app"}`)
	f.write("Code/app/src/index.js", "console.log('hello')")
	f.write("Code/app/node_modules/left-pad/index.js", "module.exports = 1")
	f.write("Code/app/dist/bundle.js", "minified")
	f.write("Code/app/.next/cache/blob", "cache")

	// A Go project.
	f.write("Code/api/go.mod", "module example.com/api")
	f.write("Code/api/main.go", "package main")
	f.write("Code/api/vendor/github.com/x/y.go", "package y")

	// Junk and caches.
	f.write("Documents/.DS_Store", "junk")
	f.write("Documents/Thumbs.db", "junk")
	f.write("Documents/real.docx", "actual work")
	f.write("AppData/Local/Temp/tmp1234", "temp")

	// A folder that merely happens to be called "build", with no project marker,
	// must be kept: it could be a builder's photo album.
	f.write("Pictures/build/facade.jpg", "photo of a building")

	f.backup()
	tree := f.latestTree()

	mustHave := []string{
		"home/Code/app/package.json",
		"home/Code/app/src/index.js",
		"home/Code/api/main.go",
		"home/Documents/real.docx",
		"home/Pictures/build/facade.jpg",
	}
	mustNotHave := []string{
		"home/Code/app/node_modules/left-pad/index.js",
		"home/Code/app/dist/bundle.js",
		"home/Code/app/.next/cache/blob",
		"home/Code/api/vendor/github.com/x/y.go",
		"home/Documents/.DS_Store",
		"home/Documents/Thumbs.db",
		"home/AppData/Local/Temp/tmp1234",
	}
	for _, path := range mustHave {
		if _, ok := tree[path]; !ok {
			t.Errorf("%s should have been backed up", path)
		}
	}
	for _, path := range mustNotHave {
		if _, ok := tree[path]; ok {
			t.Errorf("%s should have been excluded", path)
		}
	}
}

// An incremental backup must upload only what changed, and must still present a
// complete tree.
func TestIncrementalBackupUploadsOnlyChanges(t *testing.T) {
	f := newFixture(t)
	f.write("Documents/stable.txt", strings.Repeat("unchanged data\n", 5000))
	f.write("Documents/edited.txt", "version one")
	f.backup()

	usageBefore := f.storedBytes()

	f.write("Documents/edited.txt", "version two, a little longer")
	f.write("Documents/added.txt", "brand new file")
	f.backup()

	tree := f.latestTree()
	if _, ok := tree["home/Documents/stable.txt"]; !ok {
		t.Error("the unchanged file must still be part of the newest backup")
	}
	if got := tree["home/Documents/edited.txt"].Size; got != int64(len("version two, a little longer")) {
		t.Errorf("edited file has size %d, want the new version", got)
	}
	if _, ok := tree["home/Documents/added.txt"]; !ok {
		t.Error("the new file should have been backed up")
	}

	// The large unchanged file must not have been stored twice.
	growth := f.storedBytes() - usageBefore
	if growth > 4096 {
		t.Errorf("second backup stored %d extra bytes; the unchanged 70 KB file was re-uploaded", growth)
	}
}

func TestDeletedFilesDisappearFromNewestBackup(t *testing.T) {
	f := newFixture(t)
	f.write("Documents/keep.txt", "keep me")
	f.write("Documents/temporary.txt", "delete me")
	f.backup()

	if err := os.Remove(filepath.Join(f.home, "Documents", "temporary.txt")); err != nil {
		t.Fatal(err)
	}
	f.backup()

	tree := f.latestTree()
	if _, ok := tree["home/Documents/temporary.txt"]; ok {
		t.Error("a deleted file should not appear in the newest backup")
	}
	if _, ok := tree["home/Documents/keep.txt"]; !ok {
		t.Error("the remaining file should still be backed up")
	}
}

func TestEncryptedBackupRoundTrip(t *testing.T) {
	f := newFixture(t)
	key, err := codec.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.Encryption = config.Encryption{
		Enabled: true, KeyID: key.ID(), RecoveryCode: key.RecoveryCode(),
	}

	secret := "salary: confidential"
	f.write("Documents/private.txt", secret)
	f.backup()

	// The server must hold no readable copy of the plaintext.
	found := false
	err = filepath.WalkDir(filepath.Join(filepath.Dir(f.stateDir), "blobs"),
		func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			if bytes.Contains(raw, []byte(secret)) {
				found = true
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("plaintext was found in the stored blobs; encryption is not working")
	}

	// A device holding the key restores it fine.
	ctx := context.Background()
	client, _ := api.NewClient(f.cfg.ServerURL, f.cfg.DeviceToken)
	sealed, err := codec.New(codec.Options{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	defer sealed.Close()
	snap, err := restore.FindSnapshot(ctx, client, "latest")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err := restore.Run(ctx, restore.Options{
		Client: client, Codec: sealed, SnapshotID: snap.ID, Target: target,
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, "home", "Documents", "private.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != secret {
		t.Fatalf("restored %q, want %q", got, secret)
	}

	// A device with the wrong key must fail loudly rather than write garbage.
	wrongKey, err := codec.NewRandomKey()
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := codec.New(codec.Options{Key: wrongKey})
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Close()
	result, err := restore.Run(ctx, restore.Options{
		Client: client, Codec: wrong, SnapshotID: snap.ID,
		Target: filepath.Join(t.TempDir(), "wrong"),
	})
	if err == nil && len(result.Failed) == 0 {
		t.Fatal("restoring with the wrong key should have failed")
	}
}

func TestLargeFileSurvivesChunking(t *testing.T) {
	f := newFixture(t)
	// Random data so nothing is hidden by compression, and large enough to span
	// many chunks.
	big := make([]byte, 12<<20)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.home, "Videos", "clip.bin")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}

	f.backup()

	entry := f.latestTree()["home/Videos/clip.bin"]
	if entry.Size != int64(len(big)) {
		t.Fatalf("backed up size %d, want %d", entry.Size, len(big))
	}
	if len(entry.Chunks) < 3 {
		t.Fatalf("expected a 12 MiB file to span several chunks, got %d", len(entry.Chunks))
	}

	ctx := context.Background()
	client, _ := api.NewClient(f.cfg.ServerURL, f.cfg.DeviceToken)
	c, _ := codec.New(codec.Options{})
	defer c.Close()
	target := filepath.Join(t.TempDir(), "restored")
	if _, err := restore.Run(ctx, restore.Options{
		Client: client, Codec: c, SnapshotID: entrySnapshot(t, f), Target: target,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, "home", "Videos", "clip.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, big) {
		t.Fatal("the restored file does not match the original")
	}
}

// Modifying one byte in the middle of a large file must re-upload roughly one
// chunk, not the whole file. This is the promise of content-defined chunking.
func TestSmallEditReUploadsLittleData(t *testing.T) {
	f := newFixture(t)
	big := make([]byte, 20<<20)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.home, "big.bin")
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	f.backup()
	before := f.storedBytes()

	// Insert a byte in the middle: a fixed-block scheme would have to re-upload
	// everything after this point.
	edited := append([]byte{}, big[:10<<20]...)
	edited = append(edited, 0x42)
	edited = append(edited, big[10<<20:]...)
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	f.backup()

	growth := f.storedBytes() - before
	// Two chunks of slack: the edited chunk plus the boundary shift.
	if growth > 10<<20 {
		t.Errorf("a one-byte insertion re-uploaded %s; content-defined chunking is not working",
			humanSize(growth))
	}
	if growth == 0 {
		t.Error("the edit was not backed up at all")
	}
}

func TestBackupSkipsUnreadableFileAndContinues(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows permissions do not work through chmod, so an unreadable file
		// cannot be simulated this way.
		t.Skip("file permissions cannot be simulated with chmod on Windows")
	}
	f := newFixture(t)
	f.write("Documents/readable.txt", "fine")
	f.write("Documents/locked.txt", "secret")
	locked := filepath.Join(f.home, "Documents", "locked.txt")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Skipf("cannot make a file unreadable here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	// The backup must succeed despite the unreadable file.
	f.backup()
	tree := f.latestTree()
	if _, ok := tree["home/Documents/readable.txt"]; !ok {
		t.Error("one unreadable file must not stop the rest of the backup")
	}
}

func TestMissingFolderIsReportedNotFatal(t *testing.T) {
	f := newFixture(t)
	f.write("Documents/a.txt", "content")
	// A configured folder on a drive that is not connected.
	f.cfg.Roots = append(f.cfg.Roots, config.Root{
		Name: "external", Path: filepath.Join(f.home, "..", "definitely-not-here"), Enabled: true,
	})

	f.backup()

	if _, ok := f.latestTree()["home/Documents/a.txt"]; !ok {
		t.Error("a missing folder must not prevent the rest of the backup")
	}
}

// storedBytes reports how much the server has physically stored.
func (f *fixture) storedBytes() int64 {
	f.t.Helper()
	user, err := f.db.FirstUser(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	n, err := f.db.AccountStoredBytes(context.Background(), user.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

func entrySnapshot(t *testing.T, f *fixture) string {
	t.Helper()
	client, _ := api.NewClient(f.cfg.ServerURL, f.cfg.DeviceToken)
	snap, err := restore.FindSnapshot(context.Background(), client, "latest")
	if err != nil {
		t.Fatal(err)
	}
	return snap.ID
}

// compareTrees asserts that two directory trees hold identical files.
func compareTrees(t *testing.T, original, restored string) {
	t.Helper()
	err := filepath.WalkDir(original, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(original, path)
		if err != nil {
			return err
		}
		want, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(restored, rel))
		if err != nil {
			t.Errorf("%s was not restored: %v", rel, err)
			return nil
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs after restore (%d bytes vs %d)", rel, len(got), len(want))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func keys(m map[string]api.Entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func humanSize(n int64) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}
