package maintenance

import (
	"testing"
	"time"

	"github.com/foisalislambd/openbackup/internal/server/store"
)

func TestPlanRetentionKeepsWindowWhenThinningIsOff(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	rows := []store.RetentionRow{
		snap("new", "dev", "", now.Add(-24*time.Hour), false, 30, 0, 0, 0),
		snap("mid", "dev", "", now.Add(-10*24*time.Hour), false, 30, 0, 0, 0),
		snap("old", "dev", "", now.Add(-40*24*time.Hour), false, 30, 0, 0, 0),
		snap("pin", "dev", "", now.Add(-80*24*time.Hour), true, 30, 0, 0, 0),
	}
	keep := planRetention(rows, now)
	for _, id := range []string{"new", "mid", "pin"} {
		if _, ok := keep[id]; !ok {
			t.Errorf("expected %s to be kept", id)
		}
	}
	if _, ok := keep["old"]; ok {
		t.Error("snapshot past the window should be removed when it is not newest or pinned")
	}
}

func TestPlanRetentionThinsToDailyBuckets(t *testing.T) {
	now := time.Date(2026, 6, 10, 18, 0, 0, 0, time.UTC)
	rows := []store.RetentionRow{
		snap("d0a", "dev", "", now.Add(-1*time.Hour), false, 30, 2, 0, 0),
		snap("d0b", "dev", "", now.Add(-3*time.Hour), false, 30, 2, 0, 0),
		snap("d1", "dev", "", now.Add(-26*time.Hour), false, 30, 2, 0, 0),
		snap("d2", "dev", "", now.Add(-50*time.Hour), false, 30, 2, 0, 0),
	}
	keep := planRetention(rows, now)
	if _, ok := keep["d0a"]; !ok {
		t.Error("newest snapshot of the latest day should be kept")
	}
	if _, ok := keep["d0b"]; ok {
		t.Error("an older snapshot from the same day should not take another daily slot")
	}
	if _, ok := keep["d1"]; !ok {
		t.Error("the previous day should fill the second daily slot")
	}
	if _, ok := keep["d2"]; ok {
		t.Error("a third day should be dropped when only two daily copies are kept")
	}
}

func TestPlanRetentionKeepsParentOfAKeptDelta(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	rows := []store.RetentionRow{
		snap("delta", "dev", "base", now.Add(-2*time.Hour), false, 7, 1, 0, 0),
		snap("base", "dev", "", now.Add(-20*24*time.Hour), false, 7, 1, 0, 0),
	}
	keep := planRetention(rows, now)
	if _, ok := keep["delta"]; !ok {
		t.Fatal("newest snapshot was not kept")
	}
	if _, ok := keep["base"]; !ok {
		t.Error("the base of a kept delta must be kept even though it is outside the window")
	}
}

func TestPlanRetentionKeepsNewestWhenItIsTheOnlyCopy(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	rows := []store.RetentionRow{
		snap("only", "dev", "", now.Add(-400*24*time.Hour), false, 30, 0, 0, 0),
	}
	keep := planRetention(rows, now)
	if _, ok := keep["only"]; !ok {
		t.Error("the newest snapshot of a device must survive retention")
	}
}

func snap(id, device, parent string, at time.Time, pinned bool, days, daily, weekly, monthly int) store.RetentionRow {
	return store.RetentionRow{
		ID: id, UserID: "u", DeviceID: device, ParentID: parent, StartedAt: at, Pinned: pinned,
		RetentionDays: days, KeepDaily: daily, KeepWeekly: weekly, KeepMonthly: monthly,
	}
}
