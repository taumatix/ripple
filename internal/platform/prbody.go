package platform

import (
	"context"
	"errors"

	"github.com/taumatix/ripple/internal/model"
)

// ErrNoEditPR means the host cannot rewrite a pull request description through
// ripple, so a stale description stays as it is. It is not a failure.
var ErrNoEditPR = errors.New("host cannot edit pull request descriptions")

// PRBodyEditor is the optional capability to rewrite the description of a ripple PR.
// The host reads the description and hands it to edit; edit returns the new text and
// whether it changed anything, so a description whose line a person rewrote, or that
// already says the right thing, is left alone. A host implements it only when it can
// also tell its own description edits from a person's, or a rewrite would make the PR
// look touched and CloseUntouchedPR would refuse it for good.
type PRBodyEditor interface {
	// EditPRBody applies edit to the description of pr and writes the result. It
	// changes nothing, returning false, nil, when pr is no longer open, its branch is
	// not the one given, a person has edited the description, or edit reports no change.
	EditPRBody(ctx context.Context, repo model.Repo, pr PR, edit func(body string) (string, bool)) (bool, error)
}

// EditPRBody is p's EditPRBody, or ErrNoEditPR when p has none.
func EditPRBody(ctx context.Context, p Platform, repo model.Repo, pr PR, edit func(body string) (string, bool)) (bool, error) {
	e, ok := p.(PRBodyEditor)
	if !ok {
		return false, ErrNoEditPR
	}
	return e.EditPRBody(ctx, repo, pr, edit)
}

// EditPRBody routes to the host that owns repo; ErrNoEditPR for a host without the capability.
func (m *Mux) EditPRBody(ctx context.Context, repo model.Repo, pr PR, edit func(body string) (string, bool)) (bool, error) {
	p, r, err := m.route(repo)
	if err != nil {
		return false, err
	}
	return EditPRBody(ctx, p, r, pr, edit)
}
