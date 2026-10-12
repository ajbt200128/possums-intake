package telemetry

import (
	"math"
	"time"
)

// StatusClass is deliberately numeric: callers cannot supply label text.
type StatusClass uint8

const (
	Unknown StatusClass = iota
	Status2xx
	Status4xx
	Status5xx
)

var statusLabels = [4]string{"unknown", "2xx", "4xx", "5xx"}
var binLabels = [9]string{"[0,10)", "[10,50)", "[50,100)", "[100,250)", "[250,500)", "[500,1000)", "[1000,2500)", "[2500,5000)", "[5000,infinity)"}
var binEdges = [...]time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2500 * time.Millisecond, 5 * time.Second}

const (
	requestPeriod = 5 * time.Minute
	processPeriod = time.Minute
	samplePeriod  = 10 * time.Second
	timely        = time.Second
	maxExactInt   = int64(1<<53 - 1)
	maxCPU        = 1024.0
)

type requestWindow struct {
	start, last time.Time
	valid       bool
	counts      [4]int64
	bins        [4][9]int64
}
type requestRelease struct {
	start, end time.Time
	counts     [4]int64
	bins       [4][9]int64
}
type processRelease struct {
	start, end time.Time
	cpu, rss   float64
}
type batch struct {
	request *requestRelease
	process *processRelease
}

func newRequestWindow(now time.Time) requestWindow {
	start := now.Truncate(requestPeriod)
	return requestWindow{start: start, last: now, valid: now.Equal(start)}
}

// Heartbeats certify that the runtime observed the whole window, including quiet
// windows. A stalled scheduler cannot manufacture zeros or catch up lost windows.
func (w *requestWindow) advance(now time.Time) *requestRelease {
	if now.Round(0).Before(w.last.Round(0)) {
		w.valid = false
		return nil
	}
	gapOK := now.Sub(w.last) <= 2*timely && now.Round(0).Sub(w.last.Round(0)) <= 2*timely
	end := w.start.Add(requestPeriod)
	if now.Before(end) {
		w.valid = w.valid && gapOK
		w.last = now
		return nil
	}
	next := now.Truncate(requestPeriod)
	onTime := next.Equal(end) && now.Sub(end) <= timely && gapOK
	var release *requestRelease
	if w.valid && onTime {
		candidate := requestRelease{start: w.start, end: end, counts: w.counts, bins: w.bins}
		if candidate.consistent() {
			release = &candidate
		}
	}
	*w = requestWindow{start: next, last: now, valid: onTime}
	return release
}

func (w *requestWindow) observe(class StatusClass, duration time.Duration) {
	if class > Status5xx || duration < 0 {
		w.valid = false
		return
	}
	bin := len(binEdges)
	for i, edge := range binEdges {
		if duration < edge {
			bin = i
			break
		}
	}
	if w.counts[class] == maxExactInt || w.bins[class][bin] == maxExactInt {
		w.valid = false
		return
	}
	w.counts[class]++
	w.bins[class][bin]++
}
func (r requestRelease) consistent() bool {
	total := int64(0)
	for i, count := range r.counts {
		if count < 0 || count > maxExactInt || total > maxExactInt-count {
			return false
		}
		total += count
		sum := int64(0)
		for _, n := range r.bins[i] {
			if n < 0 || n > maxExactInt || sum > maxExactInt-n {
				return false
			}
			sum += n
		}
		if sum != count {
			return false
		}
	}
	return aligned(r.start, r.end, requestPeriod)
}
func aligned(start, end time.Time, period time.Duration) bool {
	return !start.IsZero() && start.Unix() >= 0 && start.Equal(start.Truncate(period)) && end.Equal(start.Add(period))
}
func finiteRange(n, max float64) bool {
	return !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= max
}

type processWindow struct {
	start, previousAt, highWater time.Time
	previousCPU                  float64
	baseline, valid              bool
	samples                      int
	cpuSum, rssSum               float64
}

func newProcessWindow(now time.Time) processWindow {
	return processWindow{start: now.Truncate(processPeriod), highWater: now, valid: now.Equal(now.Truncate(processPeriod))}
}

// at is the scheduled boundary, observed is the actual acquisition completion.
// The sample at :60 closes the previous minute and is the next minute's baseline.
func (w *processWindow) sample(at, observed time.Time, cumulativeCPU float64, rss uint64, ok bool) *processRelease {
	if !observed.Round(0).After(w.highWater.Round(0)) && w.baseline {
		w.valid = false
		return nil
	}
	if observed.Round(0).Before(w.highWater.Round(0)) {
		w.valid = false
		return nil
	}
	w.highWater = observed
	sampleOK := ok && !observed.Before(at) && observed.Sub(at) <= timely && finiteRange(cumulativeCPU, math.MaxFloat64) && rss > 0 && rss <= uint64(maxExactInt)
	if at.Before(w.start) {
		w.valid = false
		return nil
	}
	if at.After(w.start.Add(processPeriod)) {
		// No catch-up release. A new baseline can warm up at the next full boundary.
		w.start = at.Truncate(processPeriod)
		w.samples = 0
		w.cpuSum = 0
		w.rssSum = 0
		w.valid = false
		w.baseline = false
	}
	if at.Equal(w.start) {
		w.valid = sampleOK
	} else {
		expected := w.start.Add(time.Duration(w.samples+1) * samplePeriod)
		delta := observed.Sub(w.previousAt).Seconds()
		cores := 0.0
		if w.baseline && delta > 0 {
			cores = (cumulativeCPU - w.previousCPU) / delta
		}
		good := sampleOK && w.baseline && at.Equal(expected) && observed.Sub(w.previousAt) >= samplePeriod-timely && observed.Sub(w.previousAt) <= samplePeriod+timely && finiteRange(cores, maxCPU)
		w.valid = w.valid && good
		if good {
			w.cpuSum += cores
			w.rssSum += float64(rss)
		}
		w.samples++
	}
	w.baseline = sampleOK
	w.previousAt = observed
	w.previousCPU = cumulativeCPU
	if !at.Equal(w.start.Add(processPeriod)) {
		return nil
	}
	var release *processRelease
	if w.valid && w.samples == 6 {
		candidate := processRelease{start: w.start, end: at, cpu: w.cpuSum / 6, rss: w.rssSum / 6}
		if candidate.valid() {
			release = &candidate
		}
	}
	*w = processWindow{start: at, previousAt: observed, highWater: observed, previousCPU: cumulativeCPU, baseline: sampleOK, valid: sampleOK}
	return release
}
func (r processRelease) valid() bool {
	return aligned(r.start, r.end, processPeriod) && finiteRange(r.cpu, maxCPU) && finiteRange(r.rss, float64(maxExactInt)) && r.rss > 0
}
