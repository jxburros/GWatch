package checks

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"github.com/jxburros/GWatch/internal/model"
)

const pingInterval = 200 * time.Millisecond

// pingResult is the raw outcome of one ping run.
type pingResult struct {
	Sent     int
	Received int
	RTTs     []time.Duration
}

// pingFunc performs the actual ICMP exchange. Tests replace it.
var pingFunc = runPing

// runPingCheck sends Config.PingCount echo requests and summarises latency,
// jitter and loss.
func runPingCheck(ctx context.Context, check model.Check, target string, opts Options) model.Result {
	cfg := check.Config
	timeout := attemptTimeout(check)
	host, err := hostOnly(target)
	if err != nil {
		return failResult(err.Error())
	}
	count := cfg.PingCount
	if count <= 0 {
		count = defaultPingCount
	}
	if count > maxPingCount {
		count = maxPingCount
	}

	pr, err := pingFunc(ctx, host, count, timeout)
	res := model.Result{}
	if err != nil && pr.Received == 0 {
		msg := describeNetError(err, timeout)
		if !strings.HasPrefix(msg, "DNS lookup failed") && !strings.HasPrefix(msg, "timed out") {
			msg = "ping failed: " + msg
		}
		res.Message = msg
		res.Error = msg
		if pr.Sent > 0 {
			res.Details.PacketsSent = pr.Sent
			res.LossPct = fptr(100)
		}
		return res
	}
	if pr.Sent <= 0 {
		pr.Sent = count
	}
	if pr.Received > pr.Sent {
		pr.Received = pr.Sent
	}

	res.Details.PacketsSent = pr.Sent
	res.Details.PacketsReceived = pr.Received
	loss := float64(pr.Sent-pr.Received) / float64(pr.Sent) * 100
	res.LossPct = fptr(loss)

	if len(pr.RTTs) > 0 {
		rtts := make([]float64, 0, len(pr.RTTs))
		minV, maxV, sum := math.MaxFloat64, 0.0, 0.0
		for _, d := range pr.RTTs {
			v := float64(d) / float64(time.Millisecond)
			rtts = append(rtts, round1(v))
			sum += v
			if v < minV {
				minV = v
			}
			if v > maxV {
				maxV = v
			}
		}
		avg := sum / float64(len(rtts))
		res.Details.RTTs = rtts
		res.LatencyMS = fptr(avg)
		res.MinMS = fptr(minV)
		res.MaxMS = fptr(maxV)
		jitter := 0.0
		if len(pr.RTTs) >= 2 {
			var diff float64
			for i := 1; i < len(pr.RTTs); i++ {
				diff += math.Abs(float64(pr.RTTs[i]-pr.RTTs[i-1]) / float64(time.Millisecond))
			}
			jitter = diff / float64(len(pr.RTTs)-1)
		}
		res.JitterMS = fptr(jitter)
	}

	if pr.Received == 0 {
		res.Success = false
		res.Message = "no reply (100% loss)"
		res.Error = res.Message
		return res
	}

	res.Success = true
	avgText := "n/a"
	if res.LatencyMS != nil {
		avgText = fmtMS(*res.LatencyMS) + " ms"
	}
	res.Message = fmt.Sprintf("%d/%d replies, avg %s", pr.Received, pr.Sent, avgText)
	if loss > 0 {
		res.Message += fmt.Sprintf(" (%s%% loss)", fmtMS(loss))
	}
	if lossWarn := pick(cfg.PacketLossWarnPct, opts.PacketLossWarnPct); loss > 0 && lossWarn > 0 && loss >= lossWarn {
		res.Warnings = append(res.Warnings, fmt.Sprintf("Packet loss %s%% is above the %s%% warning threshold", fmtMS(loss), fmtMS(lossWarn)))
	}
	return res
}

// runPing tries pro-bing (unprivileged first on non-Windows, then privileged)
// and finally falls back to the operating system's ping command.
func runPing(ctx context.Context, host string, count int, timeout time.Duration) (pingResult, error) {
	pr, err := runProBing(ctx, host, count, timeout, runtime.GOOS == "windows")
	if err == nil {
		return pr, nil
	}
	if ctx.Err() != nil {
		return pr, err
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return pr, err
	}
	if runtime.GOOS != "windows" && isSocketPermissionError(err) {
		pr, err2 := runProBing(ctx, host, count, timeout, true)
		if err2 == nil {
			return pr, nil
		}
		err = err2
	}
	if ctx.Err() != nil {
		return pr, err
	}
	pr2, err2 := runSystemPing(ctx, host, count, timeout)
	if err2 == nil {
		return pr2, nil
	}
	return pr, fmt.Errorf("%v (system ping: %v)", err, err2)
}

func runProBing(ctx context.Context, host string, count int, timeout time.Duration, privileged bool) (pingResult, error) {
	pinger, err := probing.NewPinger(host)
	if err != nil {
		return pingResult{}, err
	}
	pinger.Count = count
	pinger.Interval = pingInterval
	pinger.Timeout = timeout
	pinger.SetPrivileged(privileged)
	if err := pinger.RunWithContext(ctx); err != nil {
		return pingResult{}, err
	}
	stats := pinger.Statistics()
	if stats == nil {
		return pingResult{}, errors.New("no ping statistics")
	}
	pr := pingResult{Sent: stats.PacketsSent, Received: stats.PacketsRecv, RTTs: stats.Rtts}
	if pr.Sent == 0 {
		return pr, errors.New("no packets were sent")
	}
	return pr, nil
}

func isSocketPermissionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "permission") || strings.Contains(msg, "not permitted") ||
		strings.Contains(msg, "socket") || strings.Contains(msg, "protocol not supported")
}

var (
	rePingTime   = regexp.MustCompile(`(?i)\btime[=<]\s*([\d.]+)\s*ms`)
	rePingLoss   = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)%\s*(?:packet\s+)?loss`)
	rePingUnix   = regexp.MustCompile(`(?i)(\d+)\s+packets\s+transmitted,\s*(\d+)\s+(?:packets\s+)?received`)
	rePingWinTx  = regexp.MustCompile(`(?i)Sent\s*=\s*(\d+)`)
	rePingWinRx  = regexp.MustCompile(`(?i)Received\s*=\s*(\d+)`)
	rePingWinLat = regexp.MustCompile(`(?i)(?:Zeit|time)[=<]\s*(\d+)\s*ms`)
)

// runSystemPing executes the OS ping command and parses its output.
func runSystemPing(ctx context.Context, host string, count int, timeout time.Duration) (pingResult, error) {
	var args []string
	switch runtime.GOOS {
	case "windows":
		args = []string{"-n", strconv.Itoa(count), "-w", strconv.Itoa(int(timeout / time.Millisecond)), host}
	case "darwin":
		args = []string{"-c", strconv.Itoa(count), "-W", strconv.Itoa(int(timeout / time.Millisecond)), host}
	default:
		secs := int(math.Ceil(timeout.Seconds()))
		if secs < 1 {
			secs = 1
		}
		args = []string{"-c", strconv.Itoa(count), "-W", strconv.Itoa(secs), host}
	}
	cmd := exec.CommandContext(ctx, "ping", args...)
	out, runErr := cmd.CombinedOutput()
	pr := parsePingOutput(string(out))
	if pr.Sent == 0 && pr.Received == 0 && len(pr.RTTs) == 0 {
		if runErr != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = runErr.Error()
			}
			if len(msg) > 200 {
				msg = msg[:200]
			}
			return pr, errors.New(msg)
		}
		return pr, errors.New("could not parse ping output")
	}
	if pr.Sent == 0 {
		pr.Sent = count
	}
	return pr, nil
}

// parsePingOutput extracts per-packet RTTs and the sent/received summary
// from Linux, macOS and Windows ping output.
func parsePingOutput(out string) pingResult {
	pr := pingResult{}
	for _, m := range rePingTime.FindAllStringSubmatch(out, -1) {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			pr.RTTs = append(pr.RTTs, time.Duration(v*float64(time.Millisecond)))
		}
	}
	if len(pr.RTTs) == 0 {
		for _, m := range rePingWinLat.FindAllStringSubmatch(out, -1) {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				pr.RTTs = append(pr.RTTs, time.Duration(v*float64(time.Millisecond)))
			}
		}
	}
	if m := rePingUnix.FindStringSubmatch(out); m != nil {
		pr.Sent, _ = strconv.Atoi(m[1])
		pr.Received, _ = strconv.Atoi(m[2])
	} else if tx := rePingWinTx.FindStringSubmatch(out); tx != nil {
		pr.Sent, _ = strconv.Atoi(tx[1])
		if rx := rePingWinRx.FindStringSubmatch(out); rx != nil {
			pr.Received, _ = strconv.Atoi(rx[1])
		}
	} else if m := rePingLoss.FindStringSubmatch(out); m != nil && len(pr.RTTs) > 0 {
		loss, _ := strconv.ParseFloat(m[1], 64)
		pr.Received = len(pr.RTTs)
		if loss < 100 {
			pr.Sent = int(math.Round(float64(pr.Received) / (1 - loss/100)))
		}
	}
	if pr.Received == 0 && len(pr.RTTs) > 0 {
		pr.Received = len(pr.RTTs)
	}
	if pr.Received > 0 && len(pr.RTTs) > pr.Received {
		pr.RTTs = pr.RTTs[:pr.Received]
	}
	return pr
}

// PingHost sends count echo requests to host and reports how many came back
// and how long each took. It is the same exchange a ping check performs,
// offered to callers outside this package (a subnet sweep, for one) so they
// share its privilege fallbacks rather than growing their own.
func PingHost(ctx context.Context, host string, count int, timeout time.Duration) (sent, received int, rtts []time.Duration, err error) {
	if count <= 0 {
		count = defaultPingCount
	}
	if count > maxPingCount {
		count = maxPingCount
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	pr, err := pingFunc(ctx, host, count, timeout)
	return pr.Sent, pr.Received, pr.RTTs, err
}
