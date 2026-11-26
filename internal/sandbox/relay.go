package sandbox

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
)

// RelayArg is the first argument of the ripple executable when it runs as the in-sandbox
// relay of the bwrap backend (see BwrapRelay). It is not a user command.
const RelayArg = "sandbox-relay"

// RunRelay is the body of `ripple sandbox-relay <port> <socket> -- <command> [args]`. It
// runs inside the sandbox's own network namespace: it listens on 127.0.0.1:<port>,
// forwards every connection to the unix socket, and runs the command with this
// process's environment and stdio until it exits. It returns the command's exit code.
// The listener is open before the command starts, so the command never races it.
func RunRelay(args []string, stderr io.Writer) int {
	if len(args) < 4 || args[2] != "--" {
		fmt.Fprintln(stderr, "usage: sandbox-relay PORT SOCKET -- COMMAND [ARG...]")
		return 2
	}
	port, err := strconv.Atoi(args[0])
	if err != nil || port < 1 || port > 65535 {
		fmt.Fprintf(stderr, "sandbox-relay: bad port %q\n", args[0])
		return 2
	}
	sock := args[1]
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		fmt.Fprintf(stderr, "sandbox-relay: %v\n", err)
		return 1
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go forward(c, sock)
		}
	}()
	cmd := exec.Command(args[3], args[4:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(stderr, "sandbox-relay: %v\n", err)
		return 127
	}
	return 0
}

// forward copies between c and a fresh connection to the gate socket; it dials only sock.
func forward(c net.Conn, sock string) {
	defer c.Close()
	u, err := net.Dial("unix", sock)
	if err != nil {
		return
	}
	defer u.Close()
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go pipe(u, c)
	go pipe(c, u)
	<-done
	<-done
}
