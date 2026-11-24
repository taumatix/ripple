package testutil

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/sumdb"
	"golang.org/x/mod/sumdb/dirhash"
	sumnote "golang.org/x/mod/sumdb/note"
)

// SumDB is a fake checksum database that vouches for whatever a GoProxy serves. The go
// command insists on a checksum database for golang.org/toolchain (GOSUMDB=off is an
// error there), so a test that downloads a Go release needs one.
type SumDB struct {
	URL string
	// Verifier is the database's public key, in the form GOSUMDB takes before the URL.
	Verifier string
}

// NewSumDB starts a checksum database whose records are computed from proxy.
func NewSumDB(t testing.TB, proxy *GoProxy) *SumDB {
	t.Helper()
	skey, vkey, err := sumnote.GenerateKey(rand.Reader, "ripple-test-sumdb")
	if err != nil {
		t.Fatal(err)
	}
	ops := sumdb.NewTestServer(skey, func(path, vers string) ([]byte, error) {
		return goSum(t.TempDir(), proxy, path, vers)
	})
	srv := httptest.NewServer(sumdb.NewServer(ops))
	t.Cleanup(srv.Close)
	return &SumDB{URL: srv.URL, Verifier: vkey}
}

// GoSumDB is the GOSUMDB value that points the go command at the fake.
func (s *SumDB) GoSumDB() string { return s.Verifier + " " + s.URL }

func goSum(tmp string, proxy *GoProxy, path, vers string) ([]byte, error) {
	get := func(suffix string) ([]byte, error) {
		resp, err := http.Get(proxy.URL + "/" + path + "/@v/" + vers + suffix)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s%s: %s", path, suffix, resp.Status)
		}
		return io.ReadAll(resp.Body)
	}
	zipData, err := get(".zip")
	if err != nil {
		return nil, err
	}
	mod, err := get(".mod")
	if err != nil {
		return nil, err
	}
	zf := filepath.Join(tmp, "m.zip")
	if err := os.WriteFile(zf, zipData, 0o644); err != nil {
		return nil, err
	}
	zh, err := dirhash.HashZip(zf, dirhash.Hash1)
	if err != nil {
		return nil, err
	}
	mh, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(string(mod))), nil
	})
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("%s %s %s\n%s %s/go.mod %s\n", path, vers, zh, path, vers, mh)), nil
}
