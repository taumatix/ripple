// Command ripple is an event-driven dependency updater.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/config"
)

const usage = `usage:
  ripple serve                          run the service (configured by RIPPLE_* environment variables)
  ripple config validate [FILE]         check a repo file (default .ripple.yaml)
  ripple config validate --server       check the RIPPLE_* environment
  ripple config example                 print a valid example file
  ripple events list                    list events that failed 3 times and were kept (dead letters)
  ripple events requeue ID|--all        make a dead event (or every one) live again; a running server picks it up
  ripple events jobs REPO               list REPO's failed jobs (the ones ripple gave up on) with their keys
  ripple events requeue-job KEY         make a failed job pending again with a fresh attempt budget
  ripple events purge [AGE]             delete dead events older than AGE (default: the retention, 30d; "0" deletes all)
  ripple version                        print the module version this binary was built from
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, out, errw io.Writer, getenv func(string) string) int {
	switch {
	case len(args) == 1 && args[0] == "serve":
		if err := serve(getenv, errw); err != nil {
			fmt.Fprintln(errw, "ripple:", err)
			return 1
		}
		return 0
	case len(args) >= 2 && args[0] == "config" && args[1] == "validate":
		return validate(args[2:], out, errw, getenv)
	case len(args) >= 1 && args[0] == "events":
		return events(args[1:], out, errw, getenv)
	case len(args) == 1 && args[0] == "version":
		fmt.Fprintln(out, "ripple", buildVersion())
		return 0
	case len(args) == 2 && args[0] == "config" && args[1] == "example":
		fmt.Fprint(out, config.Example)
		return 0
	}
	fmt.Fprint(errw, usage)
	return 2
}

// buildVersion is "(devel)" for a build from a checkout; `go install ...@vX.Y.Z` reports vX.Y.Z.
func buildVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "unknown"
}

func validate(args []string, out, errw io.Writer, getenv func(string) string) int {
	if len(args) == 1 && args[0] == "--server" {
		if _, err := config.LoadServer(getenv); err != nil {
			fmt.Fprintln(errw, "invalid server configuration:\n"+err.Error()+"\n\nVariables:\n"+config.ServerEnv)
			return 1
		}
		fmt.Fprintln(out, "server configuration ok")
		return 0
	}
	if len(args) > 1 {
		fmt.Fprint(errw, usage)
		return 2
	}
	var src config.Source
	if len(args) == 1 {
		data, err := os.ReadFile(args[0])
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(errw, err)
			return 1
		}
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(out, "%s does not exist: the defaults apply (Go module updates for the whole repository)\n", args[0])
			return 0
		}
		src = config.Source{Name: args[0], Data: data}
	} else {
		var err error
		if src, err = config.Resolve(os.ReadFile); err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		if !src.Exists() {
			fmt.Fprintf(out, "%s does not exist: the defaults apply (Go module updates for the whole repository)\n", config.FileName)
			return 0
		}
	}
	file := src.Name
	c, err := config.ParseNamed(file, src.Data)
	if err != nil {
		fmt.Fprintf(errw, "%v\n\nA valid file looks like this:\n\n%s", err, config.Example)
		return 1
	}
	fmt.Fprintf(out, "%s ok: %d update entr", file, len(c.Updates))
	if len(c.Updates) == 1 {
		fmt.Fprintln(out, "y")
	} else {
		fmt.Fprintln(out, "ies")
	}
	if w := src.Warning(); w != "" {
		fmt.Fprintln(out, "warning: "+w)
	}
	return 0
}

func serve(getenv func(string) string, errw io.Writer) error {
	s, err := config.LoadServer(getenv)
	if err != nil {
		return fmt.Errorf("invalid server configuration:\n%w\n\nVariables:\n%s", err, config.ServerEnv)
	}
	log := slog.New(slog.NewJSONHandler(errw, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, st, err := app.New(ctx, s, log)
	if err != nil {
		return err
	}
	defer st.Close()
	defer a.Close()
	ln, err := net.Listen("tcp", s.Listen)
	if err != nil {
		return err
	}
	log.Info("listening", "addr", ln.Addr().String(), "store", s.Store)
	return a.Run(ctx, ln)
}

// events is the operator's view of the dead-letter queue. It talks to the store the
// server uses, named by the same RIPPLE_STORE variables, so it needs no HTTP endpoint and
// no credentials beyond those of the store.
func events(args []string, out, errw io.Writer, getenv func(string) string) int {
	if len(args) == 0 || (args[0] != "list" && args[0] != "requeue" && args[0] != "purge" && args[0] != "jobs" && args[0] != "requeue-job") ||
		(args[0] == "list" && len(args) != 1) || ((args[0] == "requeue" || args[0] == "jobs" || args[0] == "requeue-job") && len(args) != 2) || len(args) > 2 {
		fmt.Fprint(errw, usage)
		return 2
	}
	// The store settings are the server's; the secrets that only serve() needs are not
	// required here.
	s, err := config.LoadServer(func(k string) string {
		if v := getenv(k); v != "" {
			return v
		}
		switch k {
		case "RIPPLE_WEBHOOK_SECRET", "RIPPLE_GITHUB_APP_ID", "RIPPLE_GITHUB_KEY_FILE":
			return "-"
		}
		return ""
	})
	if err != nil {
		fmt.Fprintln(errw, "invalid server configuration:\n"+err.Error()+"\n\nVariables:\n"+config.ServerEnv)
		return 1
	}
	retention := s.EventDeadRetention
	if args[0] == "purge" && len(args) == 2 {
		if retention, err = config.ParseDuration(args[1]); err != nil {
			fmt.Fprintln(errw, "ripple:", err)
			return 2
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := app.OpenStore(ctx, s)
	if err != nil {
		fmt.Fprintln(errw, "ripple:", err)
		return 1
	}
	defer st.Close()
	switch {
	case args[0] == "list":
		err = app.EventsList(ctx, st, out)
	case args[0] == "jobs":
		err = app.EventsFailedJobs(ctx, st, args[1], out)
	case args[0] == "requeue-job":
		err = app.EventsRequeueJob(ctx, st, time.Now(), args[1], out)
	case args[0] == "requeue" && args[1] == "--all":
		err = app.EventsRequeueAll(ctx, st, out)
	case args[0] == "requeue":
		err = app.EventsRequeue(ctx, st, args[1], out)
	default:
		err = app.EventsPurge(ctx, st, time.Now(), retention, out)
	}
	if err != nil {
		fmt.Fprintln(errw, "ripple:", err)
		return 1
	}
	return 0
}
