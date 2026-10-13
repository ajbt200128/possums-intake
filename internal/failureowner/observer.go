// Package failureowner contains only the shared numeric observation boundary.
// Classification is unconditional; an optional observer never controls work.
package failureowner

import "possums-intake/internal/failuretype"

type Observer interface {
	ObserveFailure(failuretype.Type)
	InvalidateFailures()
}

func Observe(observer Observer, kind failuretype.Type) {
	if observer == nil {
		return
	}
	defer func() {
		if recover() != nil {
			Invalidate(observer)
		}
	}()
	if !kind.Valid() {
		Invalidate(observer)
		return
	}
	observer.ObserveFailure(kind)
}

func Invalidate(observer Observer) {
	if observer == nil {
		return
	}
	defer func() { _ = recover() }()
	observer.InvalidateFailures()
}
