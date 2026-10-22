package stripepay

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSDKRejectsNonIntegerAndOverflowAmounts(t *testing.T) {
	for _, value := range []any{json.Number("500.5"), json.Number("18446744073709551615"), "500"} {
		f := newFixture(t)
		f.session["amount_total"] = value
		c := offlineClient(t, f.server.URL)
		captureOutput(t, func() {
			_, err := c.RetrieveEvidence(context.Background(), fixtureSession, f.purchase)
			d := requireFailure(t, err, StageSession, ConstraintDecode, OutcomeReadUnavailable)
			if d.HTTPStatus != 200 || !d.CauseUnknown {
				t.Error("SDK decode diagnostic lost observed status or cause-unknown")
			}
		})
		if len(f.snapshot()) != 1 {
			t.Fatal("malformed amount triggered extra request")
		}
	}
}
