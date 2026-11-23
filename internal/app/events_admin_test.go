package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taumatix/ripple/internal/model"
	"github.com/taumatix/ripple/internal/store"
)

func TestEventsAdmin(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b"} {
		ev := model.Event{ID: id, Seq: int64(i + 1), InstallationID: 7, Kind: "push", Repo: "acme/" + id}
		if _, err := st.EnqueueEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
		ev.Attempts, ev.LastError, ev.LastAttemptAt = 3, "boom "+id, now
		ev.DeadAt = now.Add(time.Duration(-i) * 48 * time.Hour)
		if err := st.UpdateEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := EventsList(ctx, st, &out); err != nil || !strings.Contains(out.String(), "boom a") || !strings.Contains(out.String(), "boom b") {
		t.Fatalf("list: %v %q", err, out.String())
	}
	if err := EventsRequeue(ctx, st, "nope", &out); err == nil {
		t.Fatal("requeue of an unknown id succeeded")
	}
	out.Reset()
	if err := EventsPurge(ctx, st, now, 24*time.Hour, &out); err != nil || !strings.Contains(out.String(), "purged 1 ") {
		t.Fatalf("purge: %v %q", err, out.String())
	}
	if err := EventsRequeue(ctx, st, "a", &out); err != nil {
		t.Fatal(err)
	}
	live, _ := st.ListEvents(ctx)
	if len(live) != 1 || live[0].ID != "a" || live[0].Attempts != 0 {
		t.Fatalf("live after requeue: %+v", live)
	}
	out.Reset()
	if err := EventsRequeueAll(ctx, st, &out); err != nil || !strings.Contains(out.String(), "requeued 0 ") {
		t.Fatalf("requeue all: %v %q", err, out.String())
	}
	out.Reset()
	if err := EventsList(ctx, st, &out); err != nil || !strings.Contains(out.String(), "no dead events") {
		t.Fatalf("empty list: %v %q", err, out.String())
	}
}

func TestFailedJobsAreListedAndRequeued(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	mk := func(key, pkg string, state model.JobState) model.Job {
		return model.Job{Key: key, Repo: "acme/app", Package: pkg, Version: "v1.1.0", State: state, Attempts: 5, Reason: "boom " + pkg, NotBefore: now.Add(time.Hour)}
	}
	for _, j := range []model.Job{mk("k1", "example.com/a", model.JobFailed), mk("k2", "example.com/b", model.JobPending)} {
		if _, err := st.EnqueueJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := EventsFailedJobs(ctx, st, "acme/app", &out); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "k1") || !strings.Contains(s, "boom example.com/a") || strings.Contains(s, "k2") {
		t.Fatalf("want only the failed job listed: %q", s)
	}
	if err := EventsRequeueJob(ctx, st, now, "k2", &out); err == nil {
		t.Fatal("a pending job was requeued as if it had failed")
	}
	if err := EventsRequeueJob(ctx, st, now, "nope", &out); err == nil {
		t.Fatal("an unknown job was requeued")
	}
	if err := EventsRequeueJob(ctx, st, now, "k1", &out); err != nil {
		t.Fatal(err)
	}
	due, err := st.ListDueJobs(ctx, now, 10)
	if err != nil || len(due) != 1 || due[0].Key != "k1" {
		t.Fatalf("want only the requeued job due now (k2 is deferred an hour): %v %+v", err, due)
	}
	j, _, _ := st.GetJob(ctx, "k1")
	if j.State != model.JobPending || j.Attempts != 0 || !j.NotBefore.Equal(now) {
		t.Fatalf("requeued job: %+v", j)
	}
}
