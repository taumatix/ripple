package registry

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"github.com/taumatix/ripple/internal/testutil"
)

// Whatever the tag list holds, reading only what is lexically at or after the floor finds
// the same newest tag as reading all of it. Registries list tags in lexical order, which is
// not version order ("1.100.0" sorts before "1.25.3"), so the floor must not cut those off.
func TestTagFloorNeverHidesANewerTag(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	nums := []int{0, 1, 2, 9, 10, 11, 25, 26, 99, 100, 101, 250}
	pad := func(n int) string {
		if n > 0 && n < 10 && r.Intn(2) == 0 {
			return fmt.Sprintf("0%d", n)
		}
		return fmt.Sprint(n)
	}
	for i := 0; i < 3000; i++ {
		pick := func() string {
			switch r.Intn(3) {
			case 0:
				return fmt.Sprint(nums[r.Intn(len(nums))])
			case 1:
				return fmt.Sprintf("%d.%s", nums[r.Intn(len(nums))], pad(nums[r.Intn(len(nums))]))
			}
			return fmt.Sprintf("%d.%s.%s", nums[r.Intn(len(nums))], pad(nums[r.Intn(len(nums))]), pad(nums[r.Intn(len(nums))]))
		}
		var tags []string
		for j := 0; j < 40; j++ {
			tag := pick()
			if r.Intn(4) == 0 {
				tag += "-alpine"
			}
			if r.Intn(6) == 0 {
				tag = "v" + tag
			}
			tags = append(tags, tag)
		}
		cur := tags[r.Intn(len(tags))]
		floor := TagFloor(cur)
		if floor == "" {
			t.Fatalf("no floor for %q", cur)
		}
		var kept []string
		for _, tag := range tags {
			if tag > lastBefore(floor) {
				kept = append(kept, tag)
			}
		}
		if want, got := NewestOfShape(cur, tags), NewestOfShape(cur, kept); want != got {
			t.Fatalf("cur %q floor %q: full list says %q, floor list says %q\n%v", cur, floor, want, got, tags)
		}
	}
}

func TestTagFloorOfUntrackedTagIsEmpty(t *testing.T) {
	for _, tag := range []string{"latest", "bookworm", "1.2.3.4"} {
		if f := TagFloor(tag); f != "" {
			t.Errorf("TagFloor(%q) = %q", tag, f)
		}
	}
}

// A repository whose old tags fill many pages is not read past them: the read starts at
// the floor, and the answer is the same.
func TestTagsFromSkipsPagesBelowTheFloor(t *testing.T) {
	f := testutil.NewRegistry(t)
	for i := 0; i < 40; i++ {
		f.AddTag("org/img", fmt.Sprintf("0.%d.0", i))
	}
	f.AddTag("org/img", "1.2.3")
	f.AddTag("org/img", "1.3.0")
	f.PageSize(5)
	c := clientFor(f)
	all, err := c.Tags(context.Background(), "ghcr.io/org/img")
	if err != nil {
		t.Fatal(err)
	}
	fullPages := countReqs(f, "GET /v2/org/img/tags/list")
	got, err := c.TagsFrom(context.Background(), "ghcr.io/org/img", TagFloor("1.2.3"))
	if err != nil {
		t.Fatal(err)
	}
	if want, have := NewestOfShape("1.2.3", all), NewestOfShape("1.2.3", got); want != "1.3.0" || have != want {
		t.Fatalf("full %q floor %q", want, have)
	}
	rangedPages := countReqs(f, "GET /v2/org/img/tags/list") - fullPages
	if fullPages < 9 || rangedPages > 2 {
		t.Errorf("full list took %d tag-list requests, the floored one %d", fullPages, rangedPages)
	}
	if slices.Contains(got, "0.5.0") {
		t.Errorf("tags below the floor were returned: %v", got)
	}
}

// A registry that rejects `last=` is not an error for the caller: the whole list is read.
func TestTagsFromFallsBackWhenLastIsRefused(t *testing.T) {
	f := testutil.NewRegistry(t)
	f.AddTag("org/img", "1.2.3")
	f.AddTag("org/img", "1.3.0")
	f.Refuse("last=", -1, 400, "")
	got, err := clientFor(f).TagsFrom(context.Background(), "ghcr.io/org/img", TagFloor("1.2.3"))
	if err != nil || !slices.Equal(got, []string{"1.2.3", "1.3.0"}) {
		t.Fatalf("got %v err=%v", got, err)
	}
}

// A zero-padded later number ("22.07") is newer than an unpadded one ("22.5") yet sorts
// before it, so the floor must not start above it.
func TestTagFloorKeepsZeroPaddedTags(t *testing.T) {
	if f, tag := TagFloor("22.5"), "22.07"; tag < f {
		t.Errorf("floor %q hides %q", f, tag)
	}
}
