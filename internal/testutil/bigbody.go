package testutil

import (
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
)

// PostMeasured POSTs body to url with hdr and returns the status and the bytes the
// process allocated meanwhile, server included (same process). A receiver that holds a
// body whole, in memory or read back from a spool file, allocates at least its size; one
// that parses it as it streams allocates a small fraction. Compare the second against
// the body's size, with generous slack for the client's own buffers.
func PostMeasured(t testing.TB, url string, hdr map[string]string, body string) (status int, alloc uint64) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	runtime.ReadMemStats(&after)
	return resp.StatusCode, after.TotalAlloc - before.TotalAlloc
}
