package operator

import (
	"regexp"
	"strings"

	"possums-intake/internal/ledger"
)

var recordID = regexp.MustCompile(`^[a-z0-9]{15}$`)

// Classification only; no native financial request interception.
func accountRoute(path string) (string, bool) {
	for _, name := range []string{ledger.AccountCollectionName, ledger.AccountCollectionID} {
		prefix := "/api/collections/" + name + "/records"
		if path == prefix {
			return "", true
		}
		if strings.HasPrefix(path, prefix+"/") {
			id := strings.TrimPrefix(path, prefix+"/")
			if recordID.MatchString(id) {
				return id, true
			}
		}
	}
	return "", false
}
