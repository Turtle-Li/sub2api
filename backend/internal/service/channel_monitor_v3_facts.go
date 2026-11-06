package service

import (
	"slices"
	"sort"
	"time"
)

type channelMonitorV3Acc struct {
	success    int64
	errors     int64
	ignored    int64
	categories map[string]int64
	ttft       map[int64]int64
}

func (a *channelMonitorV3Acc) counted() int64 { return a.success + a.errors }

// channelMonitorV3Accumulate folds the facts of one component into count
// slots starting at first; count 0 folds everything into a single total.
// Ignored error categories are taken out of the counted errors.
func channelMonitorV3Accumulate(cfg ChannelMonitorV3Config, component ChannelMonitorV3Component, facts *ChannelMonitorV3Facts, first time.Time, count int, interval ...time.Duration) []channelMonitorV3Acc {
	size := count
	if size < 1 {
		size = 1
	}
	accs := make([]channelMonitorV3Acc, size)
	if facts == nil {
		return accs
	}
	index := func(groupID int64, model string, slot time.Time) int {
		if groupID != component.GroupID || (component.Model != "" && model != component.Model) {
			return -1
		}
		if count == 0 {
			return 0
		}
		i := int(slot.Sub(first) / interval[0])
		if i < 0 || i >= count {
			return -1
		}
		return i
	}
	for _, f := range facts.Metrics {
		if i := index(f.GroupID, f.Model, f.Slot); i >= 0 {
			accs[i].success += f.Success
			accs[i].errors += f.Errors
		}
	}
	for _, f := range facts.Errors {
		i := index(f.GroupID, f.Model, f.Slot)
		if i < 0 {
			continue
		}
		if slices.Contains(cfg.IgnoredErrorCategories, f.Category) {
			accs[i].ignored += f.Errors
			continue
		}
		if accs[i].categories == nil {
			accs[i].categories = map[string]int64{}
		}
		accs[i].categories[f.Category] += f.Errors
	}
	for _, f := range facts.Latency {
		if i := index(f.GroupID, f.Model, f.Slot); i >= 0 {
			if accs[i].ttft == nil {
				accs[i].ttft = map[int64]int64{}
			}
			accs[i].ttft[f.UpperBound] += f.Count
		}
	}
	for i := range accs {
		accs[i].errors -= min(accs[i].ignored, accs[i].errors)
	}
	return accs
}

func channelMonitorV3Cells(cfg ChannelMonitorV3Config, component ChannelMonitorV3Component, facts *ChannelMonitorV3Facts, first time.Time, count int, interval time.Duration, admin bool) []*ChannelMonitorV3Cell {
	accs := channelMonitorV3Accumulate(cfg, component, facts, first, count, interval)
	threshold := component.DegradedTTFTMs
	if threshold <= 0 {
		threshold = cfg.DegradedTTFTMs
	}
	cells := make([]*ChannelMonitorV3Cell, count)
	for i, acc := range accs {
		counted := acc.counted()
		if counted == 0 {
			continue
		}
		cell := &ChannelMonitorV3Cell{Start: first.Add(time.Duration(i) * interval), SuccessRate: float64(acc.success) / float64(counted)}
		cell.TTFTP50Ms = channelMonitorV3P50(acc.ttft)
		cell.TopError = channelMonitorV3TopError(acc.categories)
		errorRate := float64(acc.errors) / float64(counted)
		switch {
		case counted < int64(cfg.MinRequests):
			cell.Status = ChannelMonitorV3StatusInsufficient
		case errorRate >= cfg.DownErrorRate:
			cell.Status = ChannelMonitorV3StatusDown
		case errorRate >= cfg.DegradedErrorRate || (cell.TTFTP50Ms != nil && *cell.TTFTP50Ms > int64(threshold)):
			cell.Status = ChannelMonitorV3StatusDegraded
		default:
			cell.Status = ChannelMonitorV3StatusOperational
		}
		if admin {
			cell.Requests, cell.Errors, cell.IgnoredErrors = counted, acc.errors, acc.ignored
		}
		cells[i] = cell
	}
	return cells
}

// channelMonitorV3P50 returns the histogram bucket holding the median, the
// same bucket V2 reports as first-token P50.
func channelMonitorV3P50(hist map[int64]int64) *int64 {
	var total int64
	bounds := make([]int64, 0, len(hist))
	for bound, count := range hist {
		bounds = append(bounds, bound)
		total += count
	}
	if total == 0 {
		return nil
	}
	sort.Slice(bounds, func(i, j int) bool { return bounds[i] < bounds[j] })
	var cumulative int64
	for _, bound := range bounds {
		cumulative += hist[bound]
		if cumulative*2 >= total {
			value := bound
			return &value
		}
	}
	value := bounds[len(bounds)-1]
	return &value
}

func channelMonitorV3TopError(categories map[string]int64) string {
	top, best := "", int64(0)
	for category, count := range categories {
		if count > best || (count == best && category < top) {
			top, best = category, count
		}
	}
	return top
}

// channelMonitorV3MeanAvailability averages the components that have data,
// so a busy component does not outweigh a quiet one in its category.
func channelMonitorV3MeanAvailability(items []ChannelMonitorV3ComponentStatus) *float64 {
	sum, n := 0.0, 0
	for _, item := range items {
		if item.Availability != nil {
			sum += *item.Availability
			n++
		}
	}
	if n == 0 {
		return nil
	}
	mean := sum / float64(n)
	return &mean
}

// Incidents lists outages over the history V2 keeps, ongoing ones first; see
// channelMonitorV3RunsFromCells for how down slots become incidents.
