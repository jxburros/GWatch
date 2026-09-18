package checks

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// runTCPCheck opens a TCP connection to host:port and reports the connect time.
func runTCPCheck(ctx context.Context, check model.Check, target string) model.Result {
	timeout := attemptTimeout(check)
	host, port, err := hostPort(target, check.Config.Port, 0)
	if err != nil {
		return failResult(err.Error())
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: timeout}
	t0 := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	elapsed := time.Since(t0)
	res := model.Result{}
	if err != nil {
		msg := describeNetError(err, timeout)
		switch {
		case strings.HasPrefix(msg, "connection refused"):
			msg = fmt.Sprintf("Connection refused by %s", addr)
		case strings.HasPrefix(msg, "timed out"):
			msg = fmt.Sprintf("Connection to %s %s", addr, msg)
		case strings.HasPrefix(msg, "DNS lookup failed"):
		default:
			msg = fmt.Sprintf("Connection to %s failed: %s", addr, msg)
		}
		res.Message = msg
		res.Error = msg
		return res
	}
	remote := conn.RemoteAddr().String()
	_ = conn.Close()
	res.Success = true
	res.LatencyMS = msPtr(elapsed)
	res.Details.ConnectMs = msPtr(elapsed)
	res.Details.RemoteAddr = remote
	res.Message = fmt.Sprintf("Connected to %s in %s ms", remote, fmtMS(*res.LatencyMS))
	return res
}
