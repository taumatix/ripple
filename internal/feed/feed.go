// Package feed turns a global new-version feed into releases.
package feed

import (
	"context"

	"github.com/taumatix/ripple/internal/model"
)

// Feed yields releases in chronological order starting at a cursor.
type Feed interface {
	Name() string
	Datasource() string
	// Fetch returns releases with time >= since (empty since means "from the
	// beginning of the feed"); more reports that another page is waiting.
	Fetch(ctx context.Context, since string) (releases []model.Release, more bool, err error)
}
