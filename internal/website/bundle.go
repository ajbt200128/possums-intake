package website

import "embed"

//go:embed bundle/*.el
var bundledFiles embed.FS

// Bundled returns a fresh captured copy of the local three-page fixture. It is
// not a publication store or a fallback for missing production website state.
func Bundled() (Bundle, error) {
	entries, err := bundledFiles.ReadDir("bundle")
	if err != nil {
		return nil, bundleFault()
	}
	files := make(Bundle, len(entries))
	for _, entry := range entries {
		b, e := bundledFiles.ReadFile("bundle/" + entry.Name())
		if e != nil {
			return nil, bundleFault()
		}
		files[entry.Name()] = string(b)
	}
	return files, nil
}
