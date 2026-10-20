package website

import (
	"embed"
	"net/http"
)

//go:embed assets/iosevka-term-regular.woff2 assets/iosevka-term-bold.woff2 assets/OFL.txt
var fontAssets embed.FS

// Assets are an explicit local map, not a directory/file-server capability.
func (h *Host) asset(w http.ResponseWriter, r *http.Request) {
	file, kind := "", ""
	switch r.URL.Path {
	case "/_site/fonts/iosevka-term-regular.woff2":
		file = "assets/iosevka-term-regular.woff2"
		kind = "font/woff2"
	case "/_site/fonts/iosevka-term-bold.woff2":
		file = "assets/iosevka-term-bold.woff2"
		kind = "font/woff2"
	case "/_site/fonts/OFL.txt":
		file = "assets/OFL.txt"
		kind = "text/plain; charset=utf-8"
	default:
		h.plain(w, http.StatusNotFound, h.report(r, httpFault("SITE_ASSET_MISSING")))
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		h.plain(w, 405, h.report(r, httpFault("SITE_METHOD_REFUSED")))
		return
	}
	data, err := fontAssets.ReadFile(file)
	if err != nil {
		h.plain(w, 503, h.report(r, httpFault("SITE_ASSET_UNAVAILABLE")))
		return
	}
	w.Header().Set("Content-Type", kind)
	if r.Method == "GET" {
		_, _ = w.Write(data)
	}
}
