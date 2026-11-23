package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	// DefaultReadTimeout is how long a request body may take to arrive once it holds a slot.
	DefaultReadTimeout = 30 * time.Second
	// defaultSpoolAbove is how much of a body ReadVerified holds in memory before the rest
	// goes to a temporary file. Typical push payloads are far smaller.
	defaultSpoolAbove = 64 << 10
	// readChunk is the buffer a body is streamed through.
	readChunk = 32 << 10
	// defaultDedupeMax: see Dedupe.
	defaultDedupeMax = 100_000
)

// ReadResult says what a Guard read did.
type ReadResult int

const (
	// ReadOK: the body is returned (and, for ReadVerified, authentic).
	ReadOK ReadResult = iota
	// ReadAnswered: the response is already written (413, 408, 503 or 500); do nothing more.
	ReadAnswered
	// ReadForged: ReadVerified found the body does not match its signature. Nothing is
	// written, so the host logs it and answers 401 in its own words.
	ReadForged
)

// Guard is the host-neutral part of a webhook receiver's hardening: a cap on how many
// request bodies are being read at once, a cap on each body, a deadline on how long one
// may take, and, for signed bodies, verification that does not hold a forged body in
// memory. Every host receiver embeds one, so none re-implements them. The zero value
// is ready to use; set fields before the first request.
type Guard struct {
	// MaxInflight bounds the bodies read concurrently; further requests get 503 with
	// Retry-After. Default 4.
	MaxInflight int
	// ReadTimeout bounds the whole read of one body, from when it gets a slot, so a
	// sender that trickles bytes cannot hold a slot: the read fails with 408. Default
	// DefaultReadTimeout, negative disables. It is applied with
	// http.ResponseController.SetReadDeadline, whose deadline covers "reading the entire
	// request, including the body" and is absolute, not extended by progress. A
	// ResponseWriter that does not support deadlines (a test recorder, a wrapper that does
	// not Unwrap) leaves the server's own ReadTimeout as the only bound.
	ReadTimeout time.Duration
	// SpoolAbove is how many bytes of a signed body ReadVerified keeps in memory before
	// spilling to a temporary file. Default 64 KiB.
	SpoolAbove int
	// SpoolDir is where spilled bodies go; default os.TempDir().
	SpoolDir string

	once sync.Once
	sem  chan struct{}
}

func (g *Guard) init() {
	g.once.Do(func() {
		if g.MaxInflight <= 0 {
			g.MaxInflight = defaultMaxInflight
		}
		if g.ReadTimeout == 0 {
			g.ReadTimeout = DefaultReadTimeout
		}
		if g.SpoolAbove <= 0 {
			g.SpoolAbove = defaultSpoolAbove
		}
		g.sem = make(chan struct{}, g.MaxInflight)
	})
}

// Acquire takes a read slot for r, refusing a declared body over max with 413 and a full
// house with 503. On ok the caller must call release when it has the body; release also
// lifts the read deadline, so a slow handler after the body is not cut short.
func (g *Guard) Acquire(w http.ResponseWriter, r *http.Request, max int64) (release func(), ok bool) {
	g.init()
	if r.ContentLength > max {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	select {
	case g.sem <- struct{}{}:
	default:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return nil, false
	}
	rc := http.NewResponseController(w)
	if g.ReadTimeout > 0 {
		_ = rc.SetReadDeadline(time.Now().Add(g.ReadTimeout)) // ErrNotSupported is fine: see ReadTimeout
	}
	return func() {
		if g.ReadTimeout > 0 {
			_ = rc.SetReadDeadline(time.Time{})
		}
		<-g.sem
	}, true
}

// answerReadError writes the response for a failed body read.
func answerReadError(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
	case errors.Is(err, os.ErrDeadlineExceeded):
		http.Error(w, "request body too slow", http.StatusRequestTimeout)
	default:
		http.Error(w, "unreadable payload", http.StatusBadRequest)
	}
}

// ReadBody reads a body of at most max bytes under a slot and the read deadline. Use it
// where the request was authenticated from its headers alone, before any body is read.
func (g *Guard) ReadBody(w http.ResponseWriter, r *http.Request, max int64) ([]byte, ReadResult) {
	release, ok := g.Acquire(w, r, max)
	if !ok {
		return nil, ReadAnswered
	}
	defer release()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	if err != nil {
		answerReadError(w, err)
		return nil, ReadAnswered
	}
	return body, ReadOK
}

// ReadVerified reads a body of at most max bytes while computing HMAC-SHA256 over
// prefix followed by the body, then asks authentic whether the digest is the expected
// one. A forged body is therefore never held in memory beyond SpoolAbove (plus one
// chunk) per request: the rest goes to a temporary file, deleted before returning, and
// only an authentic body is read back into memory. The cost moved to disk, bounded by
// MaxInflight * max, not removed: an HMAC cannot be checked before its last byte.
func (g *Guard) ReadVerified(w http.ResponseWriter, r *http.Request, max int64, key, prefix []byte, authentic func(sum []byte) bool) ([]byte, ReadResult) {
	release, ok := g.Acquire(w, r, max)
	if !ok {
		return nil, ReadAnswered
	}
	defer release()
	mac := hmac.New(sha256.New, key)
	mac.Write(prefix)
	sp := &spool{limit: g.SpoolAbove, dir: g.SpoolDir}
	defer sp.discard()
	src := http.MaxBytesReader(w, r.Body, max)
	buf := make([]byte, readChunk)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			mac.Write(buf[:n])
			if werr := sp.write(buf[:n]); werr != nil {
				http.Error(w, "cannot buffer payload", http.StatusInternalServerError)
				return nil, ReadAnswered
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			answerReadError(w, err)
			return nil, ReadAnswered
		}
	}
	if !authentic(mac.Sum(nil)) {
		return nil, ReadForged
	}
	body, err := sp.bytes()
	if err != nil {
		http.Error(w, "cannot buffer payload", http.StatusInternalServerError)
		return nil, ReadAnswered
	}
	return body, ReadOK
}

// spool holds a body in memory up to limit bytes, then in a temporary file.
type spool struct {
	limit int
	dir   string
	mem   bytes.Buffer
	f     *os.File
}

func (s *spool) write(p []byte) error {
	if s.f == nil && s.mem.Len()+len(p) <= s.limit {
		s.mem.Write(p)
		return nil
	}
	if s.f == nil {
		f, err := os.CreateTemp(s.dir, "ripple-webhook-*")
		if err != nil {
			return err
		}
		s.f = f
		if _, err := f.Write(s.mem.Bytes()); err != nil {
			return err
		}
		s.mem = bytes.Buffer{}
	}
	_, err := s.f.Write(p)
	return err
}

// bytes returns everything written. The file, if any, is read whole: only authentic
// bodies get here.
func (s *spool) bytes() ([]byte, error) {
	if s.f == nil {
		return s.mem.Bytes(), nil
	}
	return os.ReadFile(s.f.Name())
}

func (s *spool) discard() {
	if s.f != nil {
		s.f.Close()
		os.Remove(s.f.Name())
	}
}

// Dedupe remembers delivery identifiers so a repeat is dropped. Hosts key it on their own
// delivery id header and, where the scheme has one, the body's signature: the id is an
// unsigned header, so a captured body replayed under a new id is still caught by its
// signature. It is in memory, per process.
type Dedupe struct {
	// TTL is how long a key is remembered; default 1h, negative disables.
	TTL time.Duration
	// Max caps the keys held; beyond it dedupe fails open rather than drop a delivery.
	// Default 100000.
	Max int

	mu        sync.Mutex
	seen      map[string]time.Time
	lastPurge time.Time
}

// Claim reserves keys at now. It returns false, claiming nothing, when any non-empty key
// was already claimed and has not expired: the delivery is a repeat.
func (d *Dedupe) Claim(now time.Time, keys ...string) bool {
	ttl, max := d.TTL, d.Max
	if ttl < 0 {
		return true
	}
	if ttl == 0 {
		ttl = time.Hour
	}
	if max <= 0 {
		max = defaultDedupeMax
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen == nil {
		d.seen = map[string]time.Time{}
	}
	for _, k := range keys {
		if exp, ok := d.seen[k]; k != "" && ok && now.Before(exp) {
			return false
		}
	}
	if len(d.seen) >= max || now.Sub(d.lastPurge) > time.Minute {
		for k, exp := range d.seen {
			if !now.Before(exp) {
				delete(d.seen, k)
			}
		}
		d.lastPurge = now
	}
	for _, k := range keys {
		if k != "" && len(d.seen) < max {
			d.seen[k] = now.Add(ttl)
		}
	}
	return true
}

// Release forgets keys, so a delivery whose handling failed can be redelivered.
func (d *Dedupe) Release(keys ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, k := range keys {
		delete(d.seen, k)
	}
}
