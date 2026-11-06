package maintenance

import (
	"fmt"
	"time"

	"github.com/foisalislambd/openbackup/internal/server/store"
)

// planRetention decides which snapshots to keep.
//
// When an account has not set daily/weekly/monthly counts, every complete
// snapshot inside the retention window is kept (and the newest snapshot of
// each device is kept even outside it). When those counts are set, only the
// newest snapshot of each day, week, or month is kept, up to the count, still
// inside the window. Pinned snapshots and the parents of anything kept are
// kept too: a delta cannot be restored without its base.
func planRetention(rows []store.RetentionRow, now time.Time) map[string]struct{} {
	byID := make(map[string]store.RetentionRow, len(rows))
	byDevice := make(map[string][]store.RetentionRow)
	order := make([]string, 0, 8)
	for _, row := range rows {
		byID[row.ID] = row
		if _, seen := byDevice[row.DeviceID]; !seen {
			order = append(order, row.DeviceID)
		}
		byDevice[row.DeviceID] = append(byDevice[row.DeviceID], row)
	}

	keep := make(map[string]struct{}, len(rows))
	var markKept func(id string)
	markKept = func(id string) {
		for hops := 0; id != "" && hops < 1000; hops++ {
			if _, ok := keep[id]; ok {
				return
			}
			keep[id] = struct{}{}
			row, ok := byID[id]
			if !ok {
				return
			}
			id = row.ParentID
		}
	}

	for _, deviceID := range order {
		deviceRows := byDevice[deviceID]
		if len(deviceRows) == 0 {
			continue
		}
		// Rows are newest first. The first one is kept even when it is older than
		// the window, and it still occupies its day/week/month slot.
		policy := deviceRows[0]
		gfs := policy.KeepDaily > 0 || policy.KeepWeekly > 0 || policy.KeepMonthly > 0
		window := time.Duration(policy.RetentionDays) * 24 * time.Hour
		seenDay := map[string]struct{}{}
		seenWeek := map[string]struct{}{}
		seenMonth := map[string]struct{}{}
		dailyUsed, weeklyUsed, monthlyUsed := 0, 0, 0

		for i, row := range deviceRows {
			forced := i == 0 || row.Pinned
			if !forced && policy.RetentionDays > 0 && now.Sub(row.StartedAt) > window {
				continue
			}
			if !gfs {
				if forced || policy.RetentionDays <= 0 || now.Sub(row.StartedAt) <= window {
					markKept(row.ID)
				}
				continue
			}
			day, week, month := bucketKeys(row.StartedAt)
			take := forced
			if policy.KeepDaily > 0 {
				if _, seen := seenDay[day]; !seen {
					seenDay[day] = struct{}{}
					if dailyUsed < policy.KeepDaily {
						dailyUsed++
						take = true
					}
				}
			}
			if policy.KeepWeekly > 0 {
				if _, seen := seenWeek[week]; !seen {
					seenWeek[week] = struct{}{}
					if weeklyUsed < policy.KeepWeekly {
						weeklyUsed++
						take = true
					}
				}
			}
			if policy.KeepMonthly > 0 {
				if _, seen := seenMonth[month]; !seen {
					seenMonth[month] = struct{}{}
					if monthlyUsed < policy.KeepMonthly {
						monthlyUsed++
						take = true
					}
				}
			}
			if take {
				markKept(row.ID)
			}
		}
	}
	return keep
}

func bucketKeys(t time.Time) (day, week, month string) {
	t = t.UTC()
	year, wk := t.ISOWeek()
	return t.Format("2006-01-02"), fmt.Sprintf("%04d-W%02d", year, wk), t.Format("2006-01")
}
