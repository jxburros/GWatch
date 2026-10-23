// `gwatch-agent probe`, and the result file the Windows setup program reads.
//
// The setup program's Connect page asks for a server address and a pairing
// code, and a pairing code is good for one use. Finding out that the address
// was mistyped by spending the code on it is a poor way to learn, so the page
// has a "Test connection" button that runs this first: it proves the address
// is reachable, that its certificate is acceptable under the settings chosen,
// and that what answers is a GWatch, without presenting the code or anything
// else. It asks the one public question GWatch answers for anyone, GET
// /api/health, whose unauthenticated answer is a liveness document carrying
// "schedulerRunning"; something that answers without that key is not GWatch,
// however healthy it is.
//
// It prints one line and exits zero when the server is a GWatch it could
// reach, non-zero otherwise, so a script can use it as it stands.
//
// --result-file is how a program that cannot read a console (the setup
// program, which runs the agent hidden) learns what happened. Both probe and
// install write it: a small INI file, because Inno Setup reads INI files with
// one built-in call and has no JSON parser. On Windows it is written as UTF-16
// with a byte-order mark, which is what GetPrivateProfileStringW needs to read
// a machine name that is not plain ASCII correctly.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"
)

// resultFile is --result-file: where probe and install say how they went.
var resultFile string

// resultTarget is the configuration install worked with, so that the result
// written on the way out (from fatal, too) can name the machine GWatch paired.
var resultTarget *config

// probeServer checks that server is a reachable GWatch under cfg's TLS
// settings and returns one line saying so, or an error that says what is wrong
// in words a person can act on.
func probeServer(ctx context.Context, cfg config) (string, error) {
	if strings.TrimSpace(cfg.server) == "" {
		return "", errors.New("give the GWatch server's address with --server, e.g. https://gwatch.lan:7230")
	}
	base := cfg.baseURL()
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("%q is not a server address; write it like https://gwatch.lan:7230", cfg.server)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/health", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := newClient(cfg).Do(req)
	if err != nil {
		return "", explainProbeError(base, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("something at %s answered HTTP %d, which GWatch would not; check the address and port", base, resp.StatusCode)
	}
	var health map[string]json.RawMessage
	if json.Unmarshal(body, &health) != nil || health["schedulerRunning"] == nil {
		return "", fmt.Errorf("something answered at %s, but it is not a GWatch server; check the address and port", base)
	}
	var running bool
	_ = json.Unmarshal(health["schedulerRunning"], &running)
	if !running {
		return fmt.Sprintf("GWatch is answering at %s (its checks are paused or still starting).", base), nil
	}
	return fmt.Sprintf("GWatch is answering at %s.", base), nil
}

// explainProbeError turns a failed request into the reason, as far as it can
// be told from here.
func explainProbeError(base string, err error) error {
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verify *tls.CertificateVerificationError
	var dns *net.DNSError
	var op *net.OpError
	msg := err.Error()
	switch {
	case errors.As(err, &unknown) || errors.As(err, &verify) && strings.Contains(msg, "unknown authority"):
		return fmt.Errorf("%s answered, but its certificate is not one this computer trusts; if GWatch uses a self-signed certificate you recognise, allow it (--insecure) and the agent will pin it when it pairs", base)
	case errors.As(err, &hostname):
		return fmt.Errorf("%s answered with a certificate for a different name (%v); use the name the certificate was made for", base, hostname.Error())
	case errors.As(err, &invalid) || errors.As(err, &verify):
		return fmt.Errorf("%s answered, but its certificate was refused: %v", base, err)
	case strings.Contains(msg, "server gave HTTP response to HTTPS client"):
		return fmt.Errorf("%s speaks plain HTTP, not HTTPS; write the address with http:// at the front", strings.Replace(base, "https://", "", 1))
	case strings.Contains(msg, "certificate key changed"):
		return fmt.Errorf("%s answered with a different certificate from the one this agent has pinned: %v", base, err)
	case errors.As(err, &dns):
		return fmt.Errorf("this computer cannot find %s by name (%v); check the spelling, or use its IP address", dns.Name, dns.Err)
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "Client.Timeout"):
		return fmt.Errorf("nothing answered at %s in time; check the address and port, and that a firewall lets this computer reach it", base)
	case errors.As(err, &op) && strings.Contains(strings.ToLower(msg), "refused"):
		return fmt.Errorf("nothing is listening at %s; check the port, and that GWatch is running", base)
	}
	return fmt.Errorf("cannot reach GWatch at %s: %v", base, err)
}

// runProbe is the probe command.
func runProbe(cfg config) error {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	line, err := probeServer(ctx, cfg)
	if err != nil {
		writeResult(map[string]string{"ok": "0", "message": err.Error()})
		return err
	}
	fmt.Println(line)
	writeResult(map[string]string{"ok": "1", "message": line})
	return nil
}

// noteInstallResult writes install's outcome, for the setup program's last
// page: whether it worked, what GWatch called the machine, where to see it,
// and what the service is doing. It does nothing without --result-file.
func noteInstallResult(err error, serviceState string) {
	if resultFile == "" {
		return
	}
	r := map[string]string{"ok": "1", "service": serviceState}
	if err != nil {
		r["ok"] = "0"
		r["message"] = err.Error()
	}
	if c := resultTarget; c != nil {
		if c.pairedAs != "" {
			r["name"] = c.pairedAs
		}
		if c.server != "" {
			r["url"] = machineLink(*c)
		}
	}
	writeResult(r)
}

// machineLink is where this machine can be seen in GWatch: its node, when
// pairing said which node that is, or the list of nodes.
func machineLink(c config) string {
	if c.pairedNode > 0 {
		return c.baseURL() + "/#/nodes/" + strconv.FormatInt(c.pairedNode, 10)
	}
	return c.baseURL() + "/#/nodes"
}

// writeResult writes the result file, if one was asked for. Values are kept
// to one line each, since an INI file has no other way to hold them.
func writeResult(fields map[string]string) {
	if resultFile == "" {
		return
	}
	var sb strings.Builder
	sb.WriteString("[result]\r\n")
	for _, k := range []string{"ok", "message", "name", "url", "service"} {
		v, ok := fields[k]
		if !ok {
			continue
		}
		v = strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(v)), " ")
		sb.WriteString(k + "=" + v + "\r\n")
	}
	_ = os.WriteFile(resultFile, encodeResult(sb.String()), 0o600)
}

func encodeResult(s string) []byte {
	if runtime.GOOS != "windows" {
		return []byte(s)
	}
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2, 2+2*len(u))
	b[0], b[1] = 0xFF, 0xFE
	for _, r := range u {
		b = append(b, byte(r), byte(r>>8))
	}
	return b
}
