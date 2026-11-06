package httpapi_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/server/maintenance"
)

func TestSearchPinSettingsAndSessions(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("desk")
	id := h.backup(client, api.SnapshotFull, "", map[string][]byte{
		"Documents/taxes-2024.pdf": []byte("tax"),
		"Pictures/cat.jpg":         []byte("cat"),
	}, nil)

	var found struct {
		Entries []api.Entry `json:"entries"`
	}
	resp := h.do(http.MethodGet, "/api/v1/ui/snapshots/"+id+"/search?q=tax", nil, &found)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search status %d", resp.StatusCode)
	}
	if len(found.Entries) != 1 || found.Entries[0].Path != "Documents/taxes-2024.pdf" {
		t.Fatalf("search results = %+v", found.Entries)
	}

	resp = h.do(http.MethodPatch, "/api/v1/ui/snapshots/"+id, map[string]any{"pinned": true}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("pin status %d", resp.StatusCode)
	}
	var listed struct {
		Snapshots []api.Snapshot `json:"snapshots"`
	}
	h.do(http.MethodGet, "/api/v1/ui/snapshots", nil, &listed)
	if len(listed.Snapshots) != 1 || !listed.Snapshots[0].Pinned {
		t.Fatalf("pinned flag not stored: %+v", listed.Snapshots)
	}

	var settings struct {
		KeepDaily       int `json:"keep_daily"`
		KeepWeekly      int `json:"keep_weekly"`
		AlertAfterHours int `json:"alert_after_hours"`
		RetentionDays   int `json:"retention_days"`
	}
	resp = h.do(http.MethodPut, "/api/v1/ui/settings", map[string]any{
		"keep_daily": 14, "keep_weekly": 8, "keep_monthly": 12, "alert_after_hours": 72,
	}, &settings)
	if resp.StatusCode != http.StatusOK || settings.KeepDaily != 14 || settings.AlertAfterHours != 72 {
		t.Fatalf("settings update: status %d %+v", resp.StatusCode, settings)
	}
	h.do(http.MethodPut, "/api/v1/ui/settings", map[string]any{"retention_days": 90}, &settings)
	if settings.KeepDaily != 14 || settings.RetentionDays != 90 {
		t.Fatalf("partial settings update cleared history counts: %+v", settings)
	}

	var sessions struct {
		Sessions []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"sessions"`
	}
	h.do(http.MethodGet, "/api/v1/ui/sessions", nil, &sessions)
	if len(sessions.Sessions) != 1 || !sessions.Sessions[0].Current || sessions.Sessions[0].ID == "" {
		t.Fatalf("sessions = %+v", sessions.Sessions)
	}
	resp = h.do(http.MethodDelete, "/api/v1/ui/sessions/"+sessions.Sessions[0].ID, nil, nil)
	if resp.StatusCode == http.StatusNoContent {
		t.Fatal("deleting the current session must be refused")
	}

	var alerts struct {
		Alerts []struct {
			ID string `json:"id"`
		} `json:"alerts"`
	}
	h.do(http.MethodGet, "/api/v1/ui/alerts", nil, &alerts)

	var report struct {
		Checked int `json:"checked"`
		Failed  int `json:"failed"`
	}
	resp = h.do(http.MethodPost, "/api/v1/ui/integrity", nil, &report)
	if resp.StatusCode != http.StatusOK || report.Checked == 0 || report.Failed != 0 {
		t.Fatalf("integrity sample: status %d %+v", resp.StatusCode, report)
	}
}

func TestIntegritySampleNoticesACorruptBlob(t *testing.T) {
	h := newHarness(t)
	h.setupAccount()
	client := h.enroll("desk")
	h.backup(client, api.SnapshotFull, "", map[string][]byte{"Documents/a.txt": []byte("hello integrity")}, nil)

	// Overwrite one stored blob with bytes that are not a valid chunk.
	var digest string
	if err := h.db.SQL().QueryRow(`SELECT digest FROM chunks LIMIT 1`).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	root := h.blobs.Location()
	path := filepath.Join(root, "blobs", digest[:2], digest[2:4], digest)
	if err := os.WriteFile(path, []byte("not-a-blob"), 0o640); err != nil {
		t.Fatal(err)
	}
	run, err := maintenance.New(h.db, h.blobs, nil).VerifySample(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if run.Failed == 0 {
		t.Fatal("a replaced blob should fail the integrity sample")
	}
}
