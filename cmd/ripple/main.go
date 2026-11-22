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
	"syscall"
	_ "time/tzdata"

	"github.com/taumatix/ripple/internal/app"
	"github.com/taumatix/ripple/internal/config"
)

const usage = `usage:
  ripple serve                          run the service (configured by RIPPLE_* environment variables)
  ripple config validate [FILE]         check a repo file (default .github/ripple.yml)
  ripple config validate --server       check the RIPPLE_* environment
  ripple config example                 print a valid example file
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
	case len(args) == 2 && args[0] == "config" && args[1] == "example":
		fmt.Fprint(out, config.Example)
		return 0
	}
	fmt.Fprint(errw, usage)
	return 2
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
	file := config.FileName
	if len(args) == 1 {
		file = args[0]
	}
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(out, "%s does not exist: the defaults apply (Go module updates for the whole repository)\n", file)
		return 0
	}
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	c, err := config.Parse(data)
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
	ln, err := net.Listen("tcp", s.Listen)
	if err != nil {
		return err
	}
	log.Info("listening", "addr", ln.Addr().String(), "store", s.Store)
	return a.Run(ctx, ln)
}
