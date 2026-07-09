package server

import (
	"net/http"
	"testing"
)

// The embedded frontend must send cache validators: without them a browser
// keeps running a pre-deploy frontend indefinitely (embed.FS files have no
// mod time, so the default file server sends nothing revalidatable).
func TestAssetETagRevalidation(t *testing.T) {
	_, ts := newCallTestServer(t)

	for _, path := range []string{"/", "/js/app.js", "/js/call.js", "/js/bluenoise.js", "/js/worklets.js"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
		etag := resp.Header.Get("ETag")
		if etag == "" {
			t.Fatalf("%s: no ETag", path)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
			t.Fatalf("%s: Cache-Control = %q, want no-cache", path, cc)
		}

		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("If-None-Match", etag)
		resp2, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp2.Body.Close()
		if resp2.StatusCode != http.StatusNotModified {
			t.Fatalf("%s: conditional GET status %d, want 304", path, resp2.StatusCode)
		}

		req.Header.Set("If-None-Match", `"stale-etag"`)
		resp3, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp3.Body.Close()
		if resp3.StatusCode != http.StatusOK {
			t.Fatalf("%s: changed content GET status %d, want 200", path, resp3.StatusCode)
		}
	}
}
