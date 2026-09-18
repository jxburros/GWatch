package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// runCertCheck performs a TLS handshake only and reports the leaf certificate.
func runCertCheck(ctx context.Context, check model.Check, target string, opts Options) model.Result {
	cfg := check.Config
	timeout := attemptTimeout(check)
	host, port, err := hostPort(target, cfg.Port, 443)
	if err != nil {
		return failResult(err.Error())
	}

	info, connectDur, handshakeDur, err := probeCert(ctx, host, port, timeout)
	res := model.Result{}
	if err != nil {
		msg := describeNetError(err, timeout)
		res.Message = msg
		res.Error = msg
		return res
	}
	res.LatencyMS = msPtr(handshakeDur)
	res.Details.TLSMs = msPtr(handshakeDur)
	res.Details.ConnectMs = msPtr(connectDur)
	res.Details.Cert = info

	warnDays := pickInt(cfg.CertWarnDays, opts.DefaultCertWarn, defaultCertWarn)
	res.Warnings = append(res.Warnings, certWarnings(info, warnDays)...)

	issuer := info.Issuer
	if issuer == "" {
		issuer = "unknown issuer"
	}
	expiry := expiryPhrase(info.DaysRemaining)
	switch {
	case info.Valid:
		res.Success = true
		res.Message = fmt.Sprintf("Valid, %s (%s)", expiry, issuer)
	case cfg.IgnoreTLSErrors:
		res.Success = true
		res.Message = fmt.Sprintf("Not verified, %s (%s): %s", expiry, issuer, info.Error)
	default:
		res.Success = false
		res.Message = "Certificate invalid: " + info.Error
		res.Error = res.Message
	}
	return res
}

// probeCert connects to host:port, performs a TLS handshake without
// verification and then verifies the presented chain manually so the details
// can be reported even when the certificate is not trusted.
func probeCert(ctx context.Context, host string, port int, timeout time.Duration) (info *model.CertInfo, connectDur, handshakeDur time.Duration, err error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: timeout}
	t0 := time.Now()
	rawConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, 0, 0, err
	}
	connectDur = time.Since(t0)
	defer rawConn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = rawConn.SetDeadline(dl)
	} else {
		_ = rawConn.SetDeadline(time.Now().Add(timeout))
	}

	tcfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS10} //nolint:gosec // verified manually below
	if net.ParseIP(host) == nil {
		tcfg.ServerName = host
	}
	tconn := tls.Client(rawConn, tcfg)
	t1 := time.Now()
	if err := tconn.HandshakeContext(ctx); err != nil {
		return nil, connectDur, 0, err
	}
	handshakeDur = time.Since(t1)
	state := tconn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, connectDur, handshakeDur, fmt.Errorf("TLS: server presented no certificate")
	}
	info = certInfoFromState(&state, host, true)
	return info, connectDur, handshakeDur, nil
}

// certInfoFromState builds a CertInfo from a TLS connection state. When the
// connection was made with InsecureSkipVerify (verifyManually) the chain is
// verified here against the system roots.
func certInfoFromState(state *tls.ConnectionState, host string, verifyManually bool) *model.CertInfo {
	if state == nil || len(state.PeerCertificates) == 0 {
		return nil
	}
	leaf := state.PeerCertificates[0]
	now := time.Now()
	info := &model.CertInfo{
		Subject:       certSubject(leaf),
		Issuer:        certIssuer(leaf),
		NotBefore:     leaf.NotBefore,
		NotAfter:      leaf.NotAfter,
		DaysRemaining: daysRemaining(leaf.NotAfter, now),
		DNSNames:      leaf.DNSNames,
	}
	if leaf.SerialNumber != nil {
		info.Serial = strings.ToUpper(hex.EncodeToString(leaf.SerialNumber.Bytes()))
	}
	var verr error
	if verifyManually || len(state.VerifiedChains) == 0 {
		verr = verifyChain(state.PeerCertificates, host, now)
	} else {
		if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
			verr = x509.CertificateInvalidError{Cert: leaf, Reason: x509.Expired}
		}
	}
	if verr != nil {
		info.Valid = false
		info.Error = verr.Error()
	} else {
		info.Valid = true
	}
	return info
}

func verifyChain(certs []*x509.Certificate, host string, now time.Time) error {
	if len(certs) == 0 {
		return fmt.Errorf("no certificate presented")
	}
	opts := x509.VerifyOptions{CurrentTime: now}
	if host != "" {
		opts.DNSName = host
	}
	if len(certs) > 1 {
		opts.Intermediates = x509.NewCertPool()
		for _, c := range certs[1:] {
			opts.Intermediates.AddCert(c)
		}
	}
	_, err := certs[0].Verify(opts)
	return err
}

func certSubject(c *x509.Certificate) string {
	if c.Subject.CommonName != "" {
		return c.Subject.CommonName
	}
	if len(c.DNSNames) > 0 {
		return c.DNSNames[0]
	}
	if len(c.Subject.Organization) > 0 {
		return c.Subject.Organization[0]
	}
	return c.Subject.String()
}

func certIssuer(c *x509.Certificate) string {
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0]
	}
	if c.Issuer.CommonName != "" {
		return c.Issuer.CommonName
	}
	return c.Issuer.String()
}

func daysRemaining(notAfter, now time.Time) int {
	return int(math.Floor(notAfter.Sub(now).Hours() / 24))
}

func expiryPhrase(days int) string {
	switch {
	case days < 0:
		return fmt.Sprintf("expired %d days ago", -days)
	case days == 0:
		return "expires today"
	case days == 1:
		return "expires in 1 day"
	default:
		return fmt.Sprintf("expires in %d days", days)
	}
}

// certWarnings returns the degraded reasons for a certificate: expiring soon
// or already expired.
func certWarnings(info *model.CertInfo, warnDays int) []string {
	if info == nil {
		return nil
	}
	if warnDays <= 0 {
		warnDays = defaultCertWarn
	}
	switch {
	case info.DaysRemaining < 0:
		return []string{fmt.Sprintf("Certificate expired %d days ago", -info.DaysRemaining)}
	case info.DaysRemaining <= warnDays:
		return []string{fmt.Sprintf("Certificate expires in %d days", info.DaysRemaining)}
	}
	return nil
}
