package app

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/config"
	"github.com/taumatix/ripple/internal/webhook"
)

func TestServerHasReadDeadlines(t *testing.T) {
	s := newServer(http.NotFoundHandler())
	if s.ReadHeaderTimeout <= 0 || s.ReadTimeout <= 0 || s.WriteTimeout <= 0 || s.IdleTimeout <= 0 {
		t.Fatalf("a deadline is unset: %+v", s)
	}
}

// A client that sends half a request line and stalls is disconnected by the real server,
// not left holding a connection (and, for a body, a read slot) for ever.
func TestServerDropsAClientThatStalls(t *testing.T) {
	s := newServer(http.NotFoundHandler())
	s.ReadHeaderTimeout = 100 * time.Millisecond
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(ln)
	defer s.Close()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("POST /webhook HTTP/1.1\r\nHost: x\r\n"))
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadAll(c); err != nil {
		t.Fatalf("the server did not close the stalled connection: %v", err)
	}
}

func TestWebhookDedupeBySignatureFollowsTheConfiguration(t *testing.T) {
	defer func(old bool) { webhook.DefaultDedupeBySignature = old }(webhook.DefaultDedupeBySignature)
	configureWebhookSpool(&config.Server{WebhookDedupeSignature: true})
	if !webhook.DefaultDedupeBySignature {
		t.Fatal("on not applied")
	}
	configureWebhookSpool(&config.Server{})
	if webhook.DefaultDedupeBySignature {
		t.Fatal("off not applied")
	}
}

func TestWebhookSpoolFollowsTheConfiguration(t *testing.T) {
	defer func(old webhook.SpoolMode) { webhook.DefaultSpool = old }(webhook.DefaultSpool)
	configureWebhookSpool(&config.Server{WebhookSpool: "memory"})
	if webhook.DefaultSpool != webhook.SpoolMemory {
		t.Fatal("memory not applied")
	}
	configureWebhookSpool(&config.Server{WebhookSpool: "disk"})
	if webhook.DefaultSpool != webhook.SpoolDisk {
		t.Fatal("disk not applied")
	}
}
