package siteauth

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCustomerKeyProtocolVectors(t *testing.T) {
	material, err := deriveKey(strings.Repeat("A", 43))
	if err != nil || material.lookup != "v1_fdef11380ae70e3c25831445bd21f0fcbdbca05f006cb9fc8085778e8091274a" || material.password != "_i33WB8MUy6ZbO-brsR_vczsW21zwU87YlzDwjCPaQM" {
		t.Fatal("synthetic key protocol vector mismatch")
	}
	seen := map[string]bool{}
	for _, size := range []int{32, 53, 54, 72, 96} {
		key := base64.RawURLEncoding.EncodeToString(make([]byte, size))
		m, err := deriveKey(key)
		if err != nil || len(m.lookup) != 67 || len(m.password) != 43 || seen[m.lookup] || seen[m.password] || m.lookup == m.password {
			t.Fatal("valid gateway key range or domain separation mismatch")
		}
		seen[m.lookup], seen[m.password] = true, true
	}
	for _, key := range []string{"", strings.Repeat("A", 42), strings.Repeat("A", 129), strings.Repeat("A", 43) + "=", strings.Repeat("A", 42) + "B", strings.Repeat("A", 43) + "\n", " " + strings.Repeat("A", 43), strings.Repeat("A", 42) + "/"} {
		if _, err := deriveKey(key); err == nil {
			t.Fatal("non-gateway key accepted")
		}
	}
}
