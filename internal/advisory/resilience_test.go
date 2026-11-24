package advisory_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/advisory"
	"github.com/taumatix/ripple/internal/httpretry"
	"github.com/taumatix/ripple/internal/testutil"
)

type recordingSleep struct{ waits []time.Duration }

func (r *recordingSleep) policy(attempts int) httpretry.Policy {
	return httpretry.Policy{MaxAttempts: attempts, Sleep: func(_ context.Context, d time.Duration) error {
		r.waits = append(r.waits, d)
		return nil
	}}
}

func osvClient(o *testutil.OSV, p httpretry.Policy) *advisory.Client {
	return &advisory.Client{DumpURL: o.DumpURL(), APIURL: o.APIURL(), Retry: p}
}

func TestModifiedWaitsOutA429(t *testing.T) {
	o := testutil.NewOSV(t)
	o.Publish("GO-2026-0001", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), []byte(`{"id":"GO-2026-0001"}`))
	o.Refuse("modified_id.csv", 2, 429, "4")
	rs := &recordingSleep{}
	got, _, err := osvClient(o, rs.policy(0)).Modified(context.Background(), advisory.Position{})
	if err != nil || len(got) != 1 || got[0].ID != "GO-2026-0001" {
		t.Fatalf("got %v err=%v", got, err)
	}
	if len(rs.waits) != 2 || rs.waits[0] != 4*time.Second {
		t.Fatalf("waits %v", rs.waits)
	}
}

func TestVulnWaitsOutA503WithRetryAfter(t *testing.T) {
	o := testutil.NewOSV(t)
	o.Publish("GO-2026-0001", time.Now(), []byte(`{"id":"GO-2026-0001","summary":"x"}`))
	o.Refuse("/v1/vulns/", 1, 503, "1")
	rs := &recordingSleep{}
	v, err := osvClient(o, rs.policy(0)).Vuln(context.Background(), "GO-2026-0001")
	if err != nil || v == nil || v.ID != "GO-2026-0001" || len(rs.waits) != 1 {
		t.Fatalf("v=%v err=%v waits=%v", v, err, rs.waits)
	}
}

func TestQueryBatchResendsTheBodyAfterA429(t *testing.T) {
	o := testutil.NewOSV(t)
	o.Refuse("/v1/querybatch", 1, 429, "2")
	rs := &recordingSleep{}
	got, err := osvClient(o, rs.policy(0)).QueryBatch(context.Background(), []advisory.Query{{Name: "example.com/a", Version: "v1.0.0"}})
	if err != nil || len(got) != 1 || len(rs.waits) != 1 {
		t.Fatalf("got %v err=%v waits=%v", got, err, rs.waits)
	}
	if _, queries := o.BatchRequests(); queries != 1 {
		t.Errorf("the retried request must carry its body: server saw %d queries", queries)
	}
}

func TestOSVGivesUpNamingTheLimit(t *testing.T) {
	o := testutil.NewOSV(t)
	o.Refuse("/v1/vulns/", -1, 429, "7")
	rs := &recordingSleep{}
	_, err := osvClient(o, rs.policy(2)).Vuln(context.Background(), "GO-2026-0001")
	var le *httpretry.LimitError
	if !errors.As(err, &le) || le.Attempts != 2 || !strings.Contains(err.Error(), `Retry-After "7"`) || !strings.Contains(err.Error(), "2 attempts") {
		t.Fatalf("err=%v", err)
	}
}

func TestOSVWaitEndsWithTheContext(t *testing.T) {
	o := testutil.NewOSV(t)
	o.Refuse("modified_id.csv", -1, 429, "20")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := osvClient(o, httpretry.Policy{}).Modified(ctx, advisory.Position{})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}
}
