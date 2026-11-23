package platform

import (
	"context"
	"errors"
)

// ErrNoCompiler means a module opted into cgo (updates.cgo) but no C compiler is
// available to RunGo, so build and vet could not run honestly. Like ErrNoToolchain it
// is "could not verify", never a pass.
var ErrNoCompiler = errors.New("c compiler not available for cgo")

// GoVerify is how a repository's .ripple.yaml and the server's settings shape one
// RunGo call. The zero value is the default: cgo off, nothing private, no credential.
type GoVerify struct {
	// CGO builds with CGO_ENABLED=1 instead of 0. It needs CC (or "cc") on PATH.
	CGO bool
	// CC overrides the C compiler for a cgo build.
	CC string
	// Private are GOPRIVATE-style module patterns: the checksum database is not asked
	// about them (GONOSUMDB). They are still fetched through the configured proxy,
	// because GOVCS=*:off forbids the direct fetch GONOPROXY would need.
	Private []string
	// Username and Token authenticate to the configured proxy. They are used only
	// when Credentialed is true: a step that executes no repository code.
	Username, Token string
	Credentialed    bool
	// GoBinary, when set, is the go command to run instead of the one on PATH: the
	// release the module's go.mod names, found by FindGo. It is still run with
	// GOTOOLCHAIN=local, so nothing is downloaded.
	GoBinary string
}

type goVerifyKey struct{}

// WithGoVerify attaches v to ctx for RunGo.
func WithGoVerify(ctx context.Context, v GoVerify) context.Context {
	return context.WithValue(ctx, goVerifyKey{}, v)
}

// GoVerifyFrom returns the settings attached to ctx, or the zero value.
func GoVerifyFrom(ctx context.Context) GoVerify {
	v, _ := ctx.Value(goVerifyKey{}).(GoVerify)
	return v
}
