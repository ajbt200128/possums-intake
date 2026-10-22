package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
	"possums-intake/internal/ledger"
	"possums-intake/internal/stripepay"
)

const sandboxDevInputLimit = 16 << 10

func sandboxDevFailure(stage, constraint string) *ledger.Diagnostic {
	return runtimeFailure("STRIPE_SANDBOX_DEV_UNAVAILABLE", stage, constraint, "local_readiness_or_teardown_unconfirmed", "Local development startup cause unknown. Inspect the closed stage/constraint privately. No financial outcome is established; do not replay a purchase or restart to diagnose an unknown payment.")
}

// No dotenv evaluator: unrelated assignments are inert, and only the single
// target literal is consumed. O_NONBLOCK prevents a substituted FIFO from hanging.
func readSandboxDevKey(path string) (key string, result error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", sandboxDevFailure("stripe_dev_input", "regular_bounded_local_input")
	}
	file := os.NewFile(uintptr(fd), "private-local-input")
	defer func() {
		if file.Close() != nil && result == nil {
			key, result = "", sandboxDevFailure("stripe_dev_input", "input_closed")
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > sandboxDevInputLimit {
		return "", sandboxDevFailure("stripe_dev_input", "regular_bounded_local_input")
	}
	body, err := io.ReadAll(io.LimitReader(file, sandboxDevInputLimit+1))
	if err != nil || len(body) > sandboxDevInputLimit {
		return "", sandboxDevFailure("stripe_dev_input", "complete_bounded_local_input")
	}
	return parseSandboxDevKey(string(body))
}

func parseSandboxDevKey(body string) (string, error) {
	invalid := func() (string, error) {
		return "", sandboxDevFailure("stripe_dev_input", "one_literal_nonfixture_test_key")
	}
	if len(body) > sandboxDevInputLimit || strings.ContainsAny(body, "\x00\v\f") {
		return invalid()
	}
	key, found := "", false
	for _, line := range strings.Split(body, "\n") {
		line = strings.Trim(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			line = strings.TrimLeft(line[6:], " \t")
		}
		name, value, assigned := strings.Cut(line, "=")
		name = strings.Trim(name, " \t")
		if !assigned || !sandboxDevVariableName(name) {
			return invalid()
		}
		if name != "STRIPE_API_KEY" {
			continue // Never evaluate or export other local configuration.
		}
		if found {
			return invalid()
		}
		found = true
		value = strings.Trim(value, " \t")
		if len(value) > 0 && (value[0] == '\'' || value[0] == '"') {
			end := strings.IndexByte(value[1:], value[0])
			if end < 0 {
				return invalid()
			}
			key = value[1 : end+1]
			tail := strings.TrimLeft(value[end+2:], " \t")
			if tail != "" && !strings.HasPrefix(tail, "#") {
				return invalid()
			}
		} else {
			if index := strings.IndexByte(value, '#'); index >= 0 {
				if index == 0 || (value[index-1] != ' ' && value[index-1] != '\t') {
					return invalid()
				}
				value = strings.TrimRight(value[:index], " \t")
			}
			key = value
		}
	}
	if !found {
		return invalid()
	}
	// This placeholder validates credential shape only. It is NEVER handed to
	// the app; the app gets the actual running listener's validated secret.
	_, err := stripepay.NewSandbox(stripepay.SandboxConfig{ReturnOrigin: "https://127.0.0.1:8443", APIKey: key, WebhookSecret: "whsec_DevelopmentValidationOnlyNotAListenerSecret123"})
	if err != nil {
		return invalid()
	}
	return key, nil
}

func sandboxDevVariableName(name string) bool {
	if name == "" {
		return false
	}
	for i, b := range []byte(name) {
		if !(b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || i > 0 && b >= '0' && b <= '9') {
			return false
		}
	}
	return true
}

type sandboxDevPrivate struct{ root, home, configHome, tmp, config string }

func newSandboxDevPrivate() (private sandboxDevPrivate, result error) {
	root, err := os.MkdirTemp("", "possums-stripe-dev-")
	if err != nil {
		return private, sandboxDevFailure("stripe_dev_private", "private_temporary_directory")
	}
	private = sandboxDevPrivate{root, filepath.Join(root, "home"), filepath.Join(root, "config"), filepath.Join(root, "tmp"), filepath.Join(root, "config", "stripe.toml")}
	for _, dir := range []string{private.home, private.configHome, private.tmp} {
		if os.Mkdir(dir, 0700) != nil {
			return private, sandboxDevFailure("stripe_dev_private", "private_subdirectories")
		}
	}
	if os.WriteFile(private.config, nil, 0600) != nil {
		return private, sandboxDevFailure("stripe_dev_private", "empty_private_configuration")
	}
	return private, nil
}

func (p sandboxDevPrivate) environment(key string) []string {
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + p.home, "XDG_CONFIG_HOME=" + p.configHome, "TMPDIR=" + p.tmp, "LANG=C", "LC_ALL=C", "CI=1", "OTEL_SDK_DISABLED=true", "STRIPE_CLI_TELEMETRY_OPTOUT=1", "DO_NOT_TRACK=1"}
	if key != "" {
		env = append(env, "STRIPE_API_KEY="+key)
	}
	return env
}
