package telemetry

import (
	"math"
	"testing"
	"time"
)

var epoch = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

func closeRequests(w *requestWindow, end time.Time) *requestRelease {
	var released *requestRelease
	for at := w.last.Add(time.Second); !at.After(end); at = at.Add(time.Second) {
		if r := w.advance(at); r != nil {
			released = r
		}
	}
	return released
}
func TestRequestCompleteSparseAndZero(t *testing.T) {
	w := newRequestWindow(epoch)
	w.observe(Status2xx, 9*time.Millisecond)
	r := closeRequests(&w, epoch.Add(requestPeriod))
	if r == nil || r.counts[Status2xx] != 1 || r.bins[Status2xx][0] != 1 || !r.consistent() {
		t.Fatal("sparse complete window absent or inconsistent")
	}
	r = closeRequests(&w, epoch.Add(2*requestPeriod))
	if r == nil || r.counts != [4]int64{} {
		t.Fatal("complete observed zero window absent")
	}
}
func TestExclusiveBinsAndStatusPopulation(t *testing.T) {
	w := newRequestWindow(epoch)
	values := []time.Duration{0, 10 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2500 * time.Millisecond, 5 * time.Second}
	for class := Unknown; class <= Status5xx; class++ {
		for _, d := range values {
			w.observe(class, d)
		}
	}
	r := closeRequests(&w, epoch.Add(requestPeriod))
	if r == nil {
		t.Fatal("missing complete window")
	}
	for i, n := range r.counts {
		if n != 9 {
			t.Fatal("unmatched status population")
		}
		for _, v := range r.bins[i] {
			if v != 1 {
				t.Fatal("bin is not exclusive")
			}
		}
	}
}
func TestRequestPartialLateMissedRestartAndRollback(t *testing.T) {
	for _, name := range []string{"startup", "missed", "late", "rollback", "restart", "invalid-status", "invalid-duration"} {
		t.Run(name, func(t *testing.T) {
			w := newRequestWindow(epoch)
			switch name {
			case "startup", "restart":
				w = newRequestWindow(epoch.Add(time.Second))
			case "missed":
				w.advance(epoch.Add(3 * time.Second))
			case "late":
				closeRequests(&w, epoch.Add(requestPeriod-time.Second))
				if w.advance(epoch.Add(requestPeriod+2*time.Second)) != nil {
					t.Fatal("late released")
				}
				return
			case "rollback":
				w.advance(epoch.Add(time.Second))
				w.advance(epoch)
			case "invalid-status":
				w.observe(StatusClass(255), 0)
			case "invalid-duration":
				w.observe(Status2xx, -1)
			}
			w.observe(Status2xx, 0)
			if closeRequests(&w, epoch.Add(requestPeriod)) != nil {
				t.Fatal("partial/invalid window released")
			}
			if closeRequests(&w, epoch.Add(2*requestPeriod)) == nil {
				t.Fatal("fresh full warmup not released")
			}
		})
	}
	// A huge jump never synthesizes catch-up zeros.
	w := newRequestWindow(epoch)
	if w.advance(epoch.Add(24*time.Hour)) != nil {
		t.Fatal("catch-up release")
	}
}
func TestRequestOverflowAndInconsistency(t *testing.T) {
	w := newRequestWindow(epoch)
	w.counts[Status2xx] = maxExactInt
	w.bins[Status2xx][0] = maxExactInt
	w.observe(Status2xx, 0)
	if closeRequests(&w, epoch.Add(requestPeriod)) != nil {
		t.Fatal("overflow released")
	}
	r := requestRelease{start: epoch, end: epoch.Add(requestPeriod)}
	r.counts[0] = 1
	if r.consistent() {
		t.Fatal("unmatched bucket accepted")
	}
	r.bins[0][0] = 1
	r.counts[1] = maxExactInt
	r.bins[1][0] = maxExactInt
	if r.consistent() {
		t.Fatal("total representability overflow accepted")
	}
	r = requestRelease{start: epoch, end: epoch.Add(requestPeriod)}
	r.bins[0][0] = -1
	if r.consistent() {
		t.Fatal("negative bin accepted")
	}
}
func TestProcessCompleteMeansAndIndependentWarmup(t *testing.T) {
	w := newProcessWindow(epoch)
	if w.sample(epoch, epoch, 100, 1000, true) != nil {
		t.Fatal("baseline released")
	}
	var r *processRelease
	for i := 1; i <= 6; i++ {
		at := epoch.Add(time.Duration(i) * samplePeriod)
		r = w.sample(at, at, 100+float64(i)*5, uint64(1000+i*100), true)
		if i < 6 && r != nil {
			t.Fatal("raw/partial sample released")
		}
	}
	if r == nil || r.cpu != 0.5 || r.rss != 1350 {
		t.Fatal("incorrect process mean")
	}
	for i := 7; i <= 12; i++ {
		at := epoch.Add(time.Duration(i) * samplePeriod)
		r = w.sample(at, at, 100+float64(i)*5, 2000, true)
	}
	if r == nil || !r.start.Equal(epoch.Add(time.Minute)) || r.rss != 2000 {
		t.Fatal("overlap or wrong second mean")
	}
	// Startup samples don't make the startup minute complete.
	w = newProcessWindow(epoch.Add(time.Second))
	for i := 1; i <= 6; i++ {
		at := epoch.Add(time.Duration(i) * samplePeriod)
		r = w.sample(at, at, float64(i), 1000, true)
	}
	if r != nil {
		t.Fatal("startup partial minute released")
	}
	for i := 7; i <= 12; i++ {
		at := epoch.Add(time.Duration(i) * samplePeriod)
		r = w.sample(at, at, float64(i), 1000, true)
	}
	if r == nil {
		t.Fatal("fresh minute unavailable after warmup")
	}
}
func TestProcessInvalidSamplesWithheld(t *testing.T) {
	for _, name := range []string{"missed", "duplicate", "late", "failure", "nan", "inf", "negative", "cpu-reset", "range", "rss-zero", "rss-overflow", "rollback"} {
		t.Run(name, func(t *testing.T) {
			w := newProcessWindow(epoch)
			w.sample(epoch, epoch, 0, 1000, true)
			for i := 1; i <= 6; i++ {
				at := epoch.Add(time.Duration(i) * samplePeriod)
				observed := at
				cpu := float64(i)
				rss := uint64(1000)
				ok := true
				if i == 2 {
					switch name {
					case "missed":
						continue
					case "duplicate":
						w.sample(at, at, cpu, rss, true)
					case "late":
						observed = at.Add(2 * time.Second)
					case "failure":
						ok = false
					case "nan":
						cpu = math.NaN()
					case "inf":
						cpu = math.Inf(1)
					case "negative", "cpu-reset":
						cpu = -1
					case "range":
						cpu = 1e6
					case "rss-zero":
						rss = 0
					case "rss-overflow":
						rss = uint64(maxExactInt) + 1
					case "rollback":
						observed = epoch
					}
				}
				if w.sample(at, observed, cpu, rss, ok) != nil {
					t.Fatal("invalid minute released")
				}
			}
		})
	}
}
func TestProcessHugeJumpNoReplay(t *testing.T) {
	w := newProcessWindow(epoch)
	w.sample(epoch, epoch, 0, 1000, true)
	at := epoch.Add(time.Hour)
	if w.sample(at, at, 100, 1000, true) != nil {
		t.Fatal("missed minutes released")
	}
	for i := 1; i <= 6; i++ {
		due := at.Add(time.Duration(i) * samplePeriod)
		r := w.sample(due, due, 100+float64(i), 1000, true)
		if i == 6 && r == nil {
			t.Fatal("fresh full minute after jump missing")
		}
	}
}
