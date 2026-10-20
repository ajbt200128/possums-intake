package ledger

import "github.com/pocketbase/pocketbase/core"

// CustomerAuthApp exposes the already-open SDK app without changing native or
// accounting hooks. Use before serving and drain HTTP before closing Service.
// It creates no schema, credentials, listener or authentication policy.
func (s *Service) CustomerAuthApp() (core.App, error) {
	if !s.Ready() {
		return nil, diagnostic(CodeClosed)
	}
	return s.app, nil
}
