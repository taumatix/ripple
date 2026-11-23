package store

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// Results come back in shard order whatever order the shards finish in.
func TestFanOutKeepsShardOrder(t *testing.T) {
	const n = 16
	got, err := fanOut(context.Background(), n, 8, func(_ context.Context, i int) (int, error) {
		time.Sleep(time.Duration(n-i) * 2 * time.Millisecond) // later shards finish first
		return i, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("slot %d holds %d: %v", i, v, got)
		}
	}
}

func TestFanOutNeverExceedsBound(t *testing.T) {
	var cur, peak atomic.Int32
	_, err := fanOut(context.Background(), 20, 3, func(_ context.Context, _ int) (struct{}, error) {
		c := cur.Add(1)
		for p := peak.Load(); c > p && !peak.CompareAndSwap(p, c); p = peak.Load() {
		}
		time.Sleep(5 * time.Millisecond)
		cur.Add(-1)
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if p := peak.Load(); p > 3 || p < 2 {
		t.Fatalf("peak concurrency %d, want 2..3", p)
	}
}

// The first error is returned and cancels the shards still running; fanOut waits for them.
func TestFanOutFirstErrorCancelsTheRest(t *testing.T) {
	boom := errors.New("boom")
	var running atomic.Int32
	var cancelled atomic.Int32
	_, err := fanOut(context.Background(), 6, 6, func(ctx context.Context, i int) (int, error) {
		running.Add(1)
		defer running.Add(-1)
		if i == 0 {
			time.Sleep(10 * time.Millisecond)
			return 0, boom
		}
		select {
		case <-ctx.Done():
			cancelled.Add(1)
			return 0, ctx.Err()
		case <-time.After(5 * time.Second):
			return i, nil
		}
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom (not a sibling's context.Canceled)", err)
	}
	if running.Load() != 0 {
		t.Fatalf("%d shards still running after fanOut returned", running.Load())
	}
	if cancelled.Load() != 5 {
		t.Fatalf("%d siblings saw the cancel, want 5", cancelled.Load())
	}
}

func TestFanOutParentCancelStopsLaunching(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var started atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := fanOut(ctx, 100, 2, func(ctx context.Context, _ int) (int, error) {
			started.Add(1)
			<-ctx.Done()
			return 0, ctx.Err()
		})
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fanOut did not return after the parent was cancelled")
	}
	if s := started.Load(); s > 2 {
		t.Fatalf("%d shards started with a bound of 2", s)
	}
}

func TestFanOutSequentialWhenBoundIsOne(t *testing.T) {
	var order []int
	_, err := fanOut(context.Background(), 5, 1, func(_ context.Context, i int) (int, error) {
		order = append(order, i)
		return i, nil
	})
	if err != nil || len(order) != 5 || order[4] != 4 {
		t.Fatalf("%v %v", order, err)
	}
}

func TestTargetShardsRule(t *testing.T) {
	for _, c := range []struct{ count, k, want int }{
		{0, 100, 1}, {100, 100, 1}, {101, 100, 2}, {200, 100, 2}, {201, 100, 4}, {800, 100, 8}, {801, 100, 16},
		{1 << 40, 100, MaxShards}, {-5, 100, 1}, {10, 0, 1},
	} {
		if got := targetShards(c.count, c.k); got != c.want {
			t.Errorf("targetShards(%d,%d) = %d, want %d", c.count, c.k, got, c.want)
		}
	}
}
