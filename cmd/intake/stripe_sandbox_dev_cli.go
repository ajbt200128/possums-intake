package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"possums-intake/internal/stripepay"
)

const sandboxDevCLILimit = 1024

// The cap applies to retained bytes, not pipe drainage. Hostile output cannot
// grow memory or block Wait on an undrained application-owned pipe.
type sandboxDevCapture struct {
	bytes []byte
	over  bool
}

func (b *sandboxDevCapture) Write(p []byte) (int, error) {
	n := len(p)
	space := sandboxDevCLILimit - len(b.bytes)
	if len(p) > space {
		p, b.over = p[:space], true
	}
	b.bytes = append(b.bytes, p...)
	return n, nil
}

func sandboxDevVersion(ctx context.Context, command func(string, ...string) *exec.Cmd, binary string, private sandboxDevPrivate, timeout time.Duration) error {
	if ctx.Err() != nil {
		return nil
	}
	cmd := command(binary, "--version")
	cmd.Env = private.environment("")
	var output sandboxDevCapture
	// One shared writer makes os/exec use one joined copier, without a data race.
	cmd.Stdout, cmd.Stderr = &output, &output
	if cmd.Start() != nil {
		return sandboxDevFailure("stripe_dev_version_spawn", "pinned_cli_started_without_credentials")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return sandboxDevFailure("stripe_dev_version_wait", "completed_version_process")
		}
	case <-ctx.Done():
		if err := sandboxDevSignal(cmd); err != nil {
			<-done
			return err
		}
		<-done // Graceful cancellation has no force-kill deadline.
		return nil
	case <-timer.C:
		primary := sandboxDevFailure("stripe_dev_version", "bounded_version_completion")
		_ = sandboxDevSignal(cmd) // Primary timeout remains authoritative; still join.
		<-done
		return primary
	}
	if output.over || !(bytes.Equal(output.bytes, []byte("stripe version 1.43.2\n")) || bytes.Equal(output.bytes, []byte("stripe version 1.43.2"))) {
		return sandboxDevFailure("stripe_dev_version", "exact_pinned_cli_version")
	}
	return nil
}

func sandboxDevSignal(cmd *exec.Cmd) error {
	if err := cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return sandboxDevFailure("stripe_dev_signal", "graceful_child_signal")
	}
	return nil
}

func sandboxDevListenerArgs(config string) []string {
	return []string{"--color", "off", "--config", config, "--device-name", "possums-local-sandbox", "listen", "--skip-update", "--latest", "--events", "checkout.session.completed,checkout.session.async_payment_succeeded", "--forward-to", "http://127.0.0.1:8844/payments/stripe/webhook"}
}

type sandboxDevReady struct {
	secret string
	err    error
}

type sandboxDevCLI struct {
	cmd      *exec.Cmd
	ready    chan sandboxDevReady
	readDone chan struct{}
	waitDone chan struct{}
	readErr  error // Published by closing readDone.
	waitErr  error // Published by closing waitDone.
}

func startSandboxDevCLI(command func(string, ...string) *exec.Cmd, binary string, private sandboxDevPrivate, key string) (*sandboxDevCLI, error) {
	cmd := command(binary, sandboxDevListenerArgs(private.config)...)
	cmd.Env = private.environment(key)
	cmd.Stdout = io.Discard
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, sandboxDevFailure("stripe_dev_spawn", "private_stderr_pipe")
	}
	cmd.Stderr = writer
	if cmd.Start() != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, sandboxDevFailure("stripe_dev_spawn", "listener_process_started")
	}
	cli := &sandboxDevCLI{cmd: cmd, ready: make(chan sandboxDevReady, 1), readDone: make(chan struct{}), waitDone: make(chan struct{})}
	// Own the pipe independently of exec.Wait, which must not truncate unread
	// readiness. This writer is no longer needed in the parent after Start.
	closeErr := writer.Close()
	go cli.read(reader)
	go func() {
		cli.waitErr = cmd.Wait()
		close(cli.waitDone)
	}()
	if closeErr != nil {
		return cli, sandboxDevFailure("stripe_dev_spawn", "parent_pipe_writer_closed")
	}
	return cli, nil
}

func (cli *sandboxDevCLI) read(reader *os.File) {
	published := false
	defer func() {
		if recover() != nil {
			cli.readErr = sandboxDevFailure("stripe_dev_read", "private_pipe_read_cause_unknown")
		}
		if reader.Close() != nil && cli.readErr == nil {
			cli.readErr = sandboxDevFailure("stripe_dev_read", "private_pipe_closed")
		}
		if !published {
			cli.ready <- sandboxDevReady{err: sandboxDevFailure("stripe_dev_ready", "reviewed_bounded_readiness_required")}
		}
		close(cli.readDone)
	}()
	buffer := bufio.NewReaderSize(reader, sandboxDevCLILimit)
	secret, err := readSandboxDevReady(buffer)
	cli.ready <- sandboxDevReady{secret, err}
	published = true
	// No per-event parsing, recording or printing after readiness, including
	// when startup was refused. Drain with fixed-sized io.Copy scratch space.
	if _, err := io.Copy(io.Discard, buffer); err != nil {
		cli.readErr = sandboxDevFailure("stripe_dev_read", "listener_output_drain")
	}
}

func readSandboxDevReady(reader *bufio.Reader) (string, error) {
	for lineNumber := 0; lineNumber < 2; lineNumber++ {
		line, err := reader.ReadSlice('\n')
		if err != nil || len(line) > sandboxDevCLILimit {
			return "", sandboxDevFailure("stripe_dev_ready", "complete_bounded_readiness_line")
		}
		text := strings.TrimSuffix(string(line), "\n")
		text = strings.TrimPrefix(text, "> ")
		if text == "Getting ready..." && lineNumber == 0 {
			continue
		}
		prefix := "Ready! You are using Stripe API Version [" + stripepay.APIVersion + "]. Your webhook signing secret is "
		const suffix = " (^C to quit)"
		if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, suffix) {
			return "", sandboxDevFailure("stripe_dev_ready", "exact_pinned_api_readiness")
		}
		secret := strings.TrimSuffix(strings.TrimPrefix(text, prefix), suffix)
		if !strings.HasPrefix(secret, "whsec_") || len(secret) <= len("whsec_") || len(secret) > 256 {
			return "", sandboxDevFailure("stripe_dev_ready", "bounded_listener_signing_secret")
		}
		for _, b := range []byte(secret[len("whsec_"):]) {
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9') {
				return "", sandboxDevFailure("stripe_dev_ready", "literal_listener_signing_secret")
			}
		}
		return secret, nil
	}
	return "", sandboxDevFailure("stripe_dev_ready", "single_optional_prelude")
}

func (cli *sandboxDevCLI) live() bool {
	select {
	case <-cli.waitDone:
		return false
	default:
		return cli.cmd.Process.Signal(syscall.Signal(0)) == nil
	}
}

func (cli *sandboxDevCLI) companionFailure() error {
	select {
	case <-cli.waitDone:
		if cli.waitErr != nil {
			return sandboxDevFailure("stripe_dev_wait", "listener_exited_before_app_shutdown")
		}
		return sandboxDevFailure("stripe_dev_listener", "live_companion_required")
	case <-cli.readDone:
		if cli.readErr != nil {
			return cli.readErr
		}
		return sandboxDevFailure("stripe_dev_read", "listener_pipe_closed_before_app_shutdown")
	default:
		return sandboxDevFailure("stripe_dev_listener", "live_companion_required")
	}
}

// Caller has already joined the app. No timeout authorizes deletion, forced
// termination, abandonment of Wait, or a wrapper restart.
func (cli *sandboxDevCLI) stop() error {
	var result error
	select {
	case <-cli.waitDone:
	default:
		result = sandboxDevSignal(cli.cmd)
	}
	<-cli.waitDone
	<-cli.readDone
	if result == nil && cli.readErr != nil {
		result = cli.readErr
	}
	if result == nil && cli.waitErr != nil {
		// Signal exit is expected during a normal stop; other Wait errors are not.
		var exit *exec.ExitError
		if !errors.As(cli.waitErr, &exit) {
			result = sandboxDevFailure("stripe_dev_wait", "child_wait_completed")
		}
	}
	return result
}
