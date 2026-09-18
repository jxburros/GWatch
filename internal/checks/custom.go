// Custom check: the user supplies a command line, GWatch runs it on the
// check's schedule and parses a small status/metric contract from its
// stdout. No shell is involved on any platform — a command such as
// `powershell -File check.ps1` is executed directly, argument by argument;
// use `sh -c '...'` (or `cmd /C ...` on Windows) explicitly when a shell
// (pipes, globbing, env expansion) is actually wanted.
//
// Security note: the command runs on this machine with the GWatch service's
// own permissions. Only trusted administrators should be able to create or
// edit a custom check.
package checks

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// maxCustomOutput caps the diagnostic output kept on the result.
const maxCustomOutput = 8 << 10 // 8 KiB

// envKeyPattern matches a valid POSIX-ish environment variable name.
var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// splitCommandLine splits a command line into arguments, honouring single
// and double quotes. It never invokes a shell — quotes only group an
// argument's text, there is no expansion, globbing or piping.
func splitCommandLine(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := rune(0)
	has := false
	flush := func() {
		if has {
			out = append(out, cur.String())
			cur.Reset()
			has = false
		}
	}
	for _, r := range s {
		switch {
		case inQuote != 0:
			if r == inQuote {
				inQuote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			inQuote = r
			has = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	flush()
	return out
}

// validateCustomCheck checks that a custom check's configuration is
// complete enough to run. It never touches the filesystem or the process
// table, so it is safe to call at edit time.
func validateCustomCheck(cfg model.CheckConfig) error {
	if strings.TrimSpace(cfg.Command) == "" {
		return errors.New("a command is required")
	}
	if len(splitCommandLine(cfg.Command)) == 0 {
		return errors.New("a command is required")
	}
	if cfg.WorkDir != "" && strings.TrimSpace(cfg.WorkDir) == "" {
		return errors.New("working directory cannot be blank")
	}
	for k := range cfg.Env {
		if !envKeyPattern.MatchString(k) {
			return fmt.Errorf("invalid environment variable name %q", k)
		}
	}
	return nil
}

// customOutput is what runCustomCheck extracts from a command's stdout.
type customOutput struct {
	status    model.Status // "" = derive from exit code
	message   string
	errMsg    string
	latencyMS *float64
}

// parseCustomOutput scans output for "key=value" control lines (case
// insensitive keys: status, message, latency_ms, error) and returns them
// along with the remaining lines (everything that was not a recognised
// control line), which is kept as diagnostic output.
func parseCustomOutput(output string) (customOutput, string) {
	var out customOutput
	var rest strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		key, val, ok := strings.Cut(trimmed, "=")
		if ok {
			key = strings.ToLower(strings.TrimSpace(key))
			val = strings.TrimSpace(val)
			switch key {
			case "status":
				switch strings.ToLower(val) {
				case "up":
					out.status = model.StatusUp
				case "degraded", "warn", "warning":
					out.status = model.StatusDegraded
				case "down":
					out.status = model.StatusDown
				}
				continue
			case "message":
				out.message = val
				continue
			case "error":
				out.errMsg = val
				continue
			case "latency_ms":
				if f, err := strconv.ParseFloat(val, 64); err == nil {
					out.latencyMS = &f
				}
				continue
			}
		}
		if rest.Len() > 0 {
			rest.WriteByte('\n')
		}
		rest.WriteString(line)
	}
	return out, rest.String()
}

// runCustomCheck runs the configured command and turns its exit code and
// stdout into a model.Result. See docs/API.md for the full output contract.
func runCustomCheck(ctx context.Context, check model.Check, target string) model.Result {
	cfg := check.Config
	if err := validateCustomCheck(cfg); err != nil {
		return failResult(err.Error())
	}
	args := splitCommandLine(cfg.Command)
	for i, a := range args {
		if strings.Contains(a, "{{target}}") {
			args[i] = strings.ReplaceAll(a, "{{target}}", target)
		}
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if wd := strings.TrimSpace(cfg.WorkDir); wd != "" {
		cmd.Dir = wd
	}
	env := append(os.Environ(), "GWATCH_TARGET="+target)
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	res := model.Result{}

	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		msg := "timed out"
		res.Success = false
		res.Message = msg
		res.Error = msg
		out, rest := parseCustomOutput(buf.String())
		_ = out
		res.Details.Output = truncateOutput(rest)
		return res
	}

	exitCode := 0
	started := true
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			started = false
		}
	}

	parsed, rest := parseCustomOutput(buf.String())
	res.Details.Output = truncateOutput(rest)

	if !started {
		msg := runErr.Error()
		res.Success = false
		res.Message = msg
		res.Error = msg
		return res
	}

	status := parsed.status
	if status == "" {
		switch exitCode {
		case 0:
			status = model.StatusUp
		case 2:
			status = model.StatusDegraded
		default:
			status = model.StatusDown
		}
	}

	res.Success = status != model.StatusDown
	if status == model.StatusDegraded {
		warn := parsed.message
		if warn == "" {
			warn = parsed.errMsg
		}
		if warn == "" {
			warn = "degraded"
		}
		res.Warnings = append(res.Warnings, warn)
	}

	switch {
	case parsed.message != "":
		res.Message = parsed.message
	case !res.Success && parsed.errMsg != "":
		res.Message = parsed.errMsg
	case res.Success:
		res.Message = "OK"
	default:
		res.Message = fmt.Sprintf("exited with status %d", exitCode)
	}
	if !res.Success {
		if parsed.errMsg != "" {
			res.Error = parsed.errMsg
		} else {
			res.Error = res.Message
		}
	}

	if parsed.latencyMS != nil {
		res.LatencyMS = fptr(*parsed.latencyMS)
	} else if res.Success {
		res.LatencyMS = msPtr(elapsed)
	}

	return res
}

func truncateOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxCustomOutput {
		return s[:maxCustomOutput] + "\n… (truncated)"
	}
	return s
}
