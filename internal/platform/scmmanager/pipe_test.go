package scmmanager_test

import (
	"io"
	"sync"
)

// blockingReader blocks its first Read until released, to hold a receiver's body
// buffer slot open.
type blockingReader struct {
	started chan struct{}
	gate    chan struct{}
	once    sync.Once
}

func newPipe() (io.Reader, *blockingReader) {
	b := &blockingReader{started: make(chan struct{}), gate: make(chan struct{})}
	return b, b
}

func (b *blockingReader) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.gate
	return 0, io.EOF
}

func (b *blockingReader) release() { close(b.gate) }
