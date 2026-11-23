package feed_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/taumatix/ripple/internal/feed"
	"github.com/taumatix/ripple/internal/testutil"
)

func TestGoDLSupported(t *testing.T) {
	fake := testutil.NewGoDL(t)
	fake.Set("go2.9.4", "go2.8.11", "go2.10rc1", "go1.21")
	got, err := (&feed.GoDL{BaseURL: fake.URL}).Supported(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"v2.9.4", "v2.8.11", "v1.21"}; !reflect.DeepEqual(got, want) {
		t.Errorf("want %v (stable only, semver), got %v", want, got)
	}
}

func TestGoDLRejectsAnEmptyOrBrokenList(t *testing.T) {
	fake := testutil.NewGoDL(t)
	fake.Set("go2.10rc1")
	if _, err := (&feed.GoDL{BaseURL: fake.URL}).Supported(context.Background()); err == nil {
		t.Error("a list with no stable release must be an error, not 'nothing is supported'")
	}
	if _, err := (&feed.GoDL{BaseURL: fake.URL + "/nope"}).Supported(context.Background()); err == nil {
		t.Error("a 404 must be an error")
	}
}
