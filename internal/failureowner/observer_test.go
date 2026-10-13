package failureowner

import (
	"possums-intake/internal/failuretype"
	"testing"
)

type observer struct {
	kinds                         []failuretype.Type
	invalid                       int
	panicObserve, panicInvalidate bool
}

func (o *observer) ObserveFailure(kind failuretype.Type) {
	if o.panicObserve {
		panic("SYNTHETIC_PRIVATE_SENTINEL")
	}
	o.kinds = append(o.kinds, kind)
}
func (o *observer) InvalidateFailures() {
	o.invalid++
	if o.panicInvalidate {
		panic("SYNTHETIC_PRIVATE_SENTINEL")
	}
}
func TestNumericObserverNilInvalidAndHostile(t *testing.T) {
	Observe(nil, failuretype.SQLiteBusy)
	Invalidate(nil)
	o := &observer{}
	Observe(o, failuretype.SQLiteBusy)
	Observe(o, 0)
	Observe(o, 40)
	if len(o.kinds) != 1 || o.kinds[0] != failuretype.SQLiteBusy || o.invalid != 2 {
		t.Fatal("numeric validity drifted")
	}
	o.panicObserve, o.panicInvalidate = true, true
	Observe(o, failuretype.SQLiteBusy)
	Invalidate(o)
	if o.invalid != 4 {
		t.Fatal("observer exceptions changed owner control flow")
	}
}
