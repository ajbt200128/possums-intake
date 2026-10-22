package payments

// TopupResult is trusted host/session data, never a public JSON response.
// Neither a redirect nor a local status read authorizes funding.
type TopupResult struct {
	Reference string // Private memory-only session selector; never render.
	URL       string // Validated transient redirect; never persist or cache.
	Status    string // Only closed local statuses may be presented.
}
