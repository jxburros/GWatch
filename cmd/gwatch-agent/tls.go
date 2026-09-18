package main

import "crypto/tls"

// insecureTLS is what --insecure turns on: the agent still uses TLS, it just
// stops verifying the certificate chain. It is here in its own file, called
// from one place, so that the one deliberate exception to certificate
// verification is easy to find and to audit.
//
// It exists because a GWatch on a home network is often behind a self-signed
// certificate. Turning it on means anyone able to intercept the connection can
// read the readings and feed the server false ones — nothing more, since the
// token grants nothing else.
func insecureTLS() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} //nolint:gosec // user opt-in, documented above
}
