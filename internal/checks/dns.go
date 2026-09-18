package checks

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// resolverAddr normalises a "host[:port]" resolver address, defaulting to
// port 53.
func resolverAddr(server string) (string, error) {
	s := strings.TrimSpace(server)
	if s == "" {
		return "", fmt.Errorf("resolver address is empty")
	}
	if strings.Contains(s, "://") {
		return "", fmt.Errorf("DNS server must be a host or host:port, not a URL")
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		// No port (or an IPv6 literal without brackets).
		host = strings.Trim(s, "[]")
		port = "53"
	}
	if host == "" {
		return "", fmt.Errorf("DNS server %q has no host", server)
	}
	if p, err := strconv.Atoi(port); err != nil || p <= 0 || p > 65535 {
		return "", fmt.Errorf("DNS server %q has an invalid port", server)
	}
	return net.JoinHostPort(host, port), nil
}

// runDNSCheck resolves the target, optionally through a specific server and
// optionally comparing the answer with expected values.
func runDNSCheck(ctx context.Context, check model.Check, target string) model.Result {
	cfg := check.Config
	timeout := attemptTimeout(check)
	name, err := hostOnly(target)
	if err != nil {
		return failResult(err.Error())
	}

	resolver := net.DefaultResolver
	resolverLabel := "system"
	if strings.TrimSpace(cfg.DNSServer) != "" {
		addr, err := resolverAddr(cfg.DNSServer)
		if err != nil {
			return failResult(err.Error())
		}
		resolverLabel = addr
		resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: timeout}
				return d.DialContext(ctx, network, addr)
			},
		}
	}

	recordType := strings.ToUpper(strings.TrimSpace(cfg.RecordType))
	if recordType == "" {
		recordType = "A"
	}

	var values []string
	t0 := time.Now()
	switch recordType {
	case "A", "AAAA":
		ips, lerr := resolver.LookupIPAddr(ctx, name)
		err = lerr
		for _, ip := range ips {
			if recordType == "AAAA" && ip.IP.To4() != nil {
				continue
			}
			values = append(values, ip.IP.String())
		}
		sort.Strings(values)
	case "CNAME":
		cname, lerr := resolver.LookupCNAME(ctx, name)
		err = lerr
		if cname != "" {
			values = append(values, strings.TrimSuffix(cname, "."))
		}
	case "MX":
		mxs, lerr := resolver.LookupMX(ctx, name)
		err = lerr
		for _, mx := range mxs {
			if mx != nil {
				values = append(values, strings.TrimSuffix(mx.Host, "."))
			}
		}
	case "TXT":
		txts, lerr := resolver.LookupTXT(ctx, name)
		err = lerr
		values = append(values, txts...)
	default:
		return failResult(fmt.Sprintf("unsupported DNS record type %q", cfg.RecordType))
	}
	elapsed := time.Since(t0)

	res := model.Result{}
	res.Details.Resolver = resolverLabel
	res.Details.ResolvedValues = values
	if err != nil {
		msg := describeNetError(err, timeout)
		if !strings.HasPrefix(msg, "DNS lookup failed") {
			msg = "DNS lookup failed: " + msg
		}
		res.Message = msg
		res.Error = msg
		return res
	}
	res.LatencyMS = msPtr(elapsed)
	if len(values) == 0 {
		msg := fmt.Sprintf("No %s records found for %s", recordType, name)
		res.Message = msg
		res.Error = msg
		return res
	}

	if len(cfg.ExpectedIPs) > 0 {
		match := true
		var missing []string
		for _, exp := range cfg.ExpectedIPs {
			e := normalizeDNSValue(exp)
			if e == "" {
				continue
			}
			found := false
			for _, v := range values {
				if normalizeDNSValue(v) == e {
					found = true
					break
				}
			}
			if !found {
				match = false
				missing = append(missing, strings.TrimSpace(exp))
			}
		}
		res.Details.ExpectedMatch = bptr(match)
		if !match {
			msg := fmt.Sprintf("Resolved to %s but expected %s", strings.Join(values, ", "), strings.Join(missing, ", "))
			res.Message = msg
			res.Error = msg
			return res
		}
	}

	res.Success = true
	res.Message = fmt.Sprintf("Resolved to %s in %s ms", strings.Join(values, ", "), fmtMS(*res.LatencyMS))
	return res
}

func normalizeDNSValue(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimSuffix(v, ".")
	if ip := net.ParseIP(v); ip != nil {
		return ip.String()
	}
	return v
}
