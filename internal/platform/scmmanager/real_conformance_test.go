//go:build scmreal

package scmmanager_test

import (
	"testing"

	"github.com/taumatix/ripple/internal/platform/platformtest"
)

// TestRealConformance is the whole conformance suite against a real SCM-Manager with the Review and
// Webhook plugins installed through its API: real git pushes, the instance's own webhooks, pull
// requests created and read through the REST API. It includes the push that must open exactly one
// pull request (ReleaseOpensOnePRInExactlyTheImportingRepos, PushRescansAndAnIrrelevantPushCostsNothing).
func TestRealConformance(t *testing.T) {
	platformtest.Run(t, &realFixture{admin: realSCM(t)})
}
