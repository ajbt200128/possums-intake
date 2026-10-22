package ledger

import (
	"strings"
	"testing"
)

func TestSyntheticPaymentCaseVariantSchemaFailsClosed(t *testing.T) {
	for _, name := range []string{"PAYMENT_META", "Payment_Meta"} {
		t.Run(name, func(t *testing.T) {
			s, dir, _ := testService(t)
			if _, err := s.app.DB().NewQuery("CREATE TABLE " + name + " (singleton INTEGER)").Execute(); err != nil {
				t.Fatal("case schema fixture")
			}
			codeIs(t, s.InitializeSyntheticPayments(background), CodeSchema)
			if err := s.Close(); err != nil {
				t.Fatal("close case fixture")
			}
			opened, err := Open(background, dir)
			if opened != nil {
				opened.Close()
				t.Fatal("case partial schema opened")
			}
			codeIs(t, err, CodeSchema)
		})
	}
	t.Run("complete_uppercase", func(t *testing.T) {
		s, dir, _ := testService(t)
		ddl := paymentSchemaV1
		for _, name := range []string{"payment_meta", "payment_sales", "payment_purchases", "payment_purchases_account", "payment_purchases_prune", "payment_sales_prune"} {
			ddl = strings.ReplaceAll(ddl, name, strings.ToUpper(name))
		}
		if _, err := s.app.DB().NewQuery(ddl).Execute(); err != nil {
			t.Fatal("complete case fixture")
		}
		if err := s.Close(); err != nil {
			t.Fatal("close complete case fixture")
		}
		opened, err := Open(background, dir)
		if opened != nil {
			opened.Close()
			t.Fatal("case complete schema opened")
		}
		codeIs(t, err, CodeSchema)
	})
}
