// Command gwatch-sign generates the GWatch release signing key and signs
// release assets with it. The signature format and all the crypto live in
// internal/update, so the running program verifies with exactly the code that
// signed.
//
// Usage:
//
//	gwatch-sign keygen -out release.key      generate a key pair
//	gwatch-sign pubkey -key release.key      print the ed25519:... line
//	gwatch-sign sign -key release.key FILE…  write FILE.sig for each file
//	gwatch-sign verify -pub ed25519:… FILE…  check FILE against FILE.sig
//
// The private key may also come from the GWATCH_SIGNING_KEY environment
// variable (a base64 32-byte seed), which is how CI reads it from a repository
// secret without ever writing it to disk.
package main

import (
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jxburros/GWatch/internal/update"
)

const envKey = "GWATCH_SIGNING_KEY"

const usage = `gwatch-sign — sign GWatch release assets

  gwatch-sign keygen -out release.key        generate a signing key pair
  gwatch-sign pubkey [-key release.key]      print the ed25519:... line for release_keys.txt
  gwatch-sign sign [-key release.key] FILE…  write FILE` + update.SignatureExt + ` next to each FILE
  gwatch-sign verify -pub ed25519:... FILE…  verify FILE against FILE` + update.SignatureExt + `

The private key is read from -key, or from the ` + envKey + ` environment
variable (base64 32-byte seed) when -key is not given.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gwatch-sign: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return errors.New("no command given")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "keygen":
		return keygen(rest)
	case "pubkey":
		return pubkey(rest)
	case "sign":
		return sign(rest)
	case "verify":
		return verify(rest)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	out := fs.String("out", "release.key", "file to write the private key to")
	force := fs.Bool("force", false, "overwrite an existing key file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(*out); err == nil && !*force {
		return fmt.Errorf("%s already exists; refusing to overwrite a signing key (use -force if you really mean it)", *out)
	}
	pub, priv, err := update.GenerateKey()
	if err != nil {
		return err
	}
	body := "# GWatch release signing key — keep this file secret and offline.\n" +
		"# Public key: " + update.FormatPublicKey(pub) + "\n" +
		update.EncodeSeed(priv) + "\n"
	if err := os.WriteFile(*out, []byte(body), 0o600); err != nil {
		return err
	}
	fmt.Printf("wrote %s (mode 0600) — keep it offline, it is the only thing that can sign a release\n", *out)
	fmt.Printf("key id: %s\n", update.KeyID(pub))
	fmt.Printf("\nPaste this line into internal/update/release_keys.txt:\n\n%s\n", update.FormatPublicKey(pub))
	fmt.Printf("\nAnd store this value as the %s repository secret:\n\n%s\n", envKey, update.EncodeSeed(priv))
	return nil
}

func pubkey(args []string) error {
	fs := flag.NewFlagSet("pubkey", flag.ContinueOnError)
	keyFile := fs.String("key", "", "private key file (default: the "+envKey+" environment variable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	priv, err := loadKey(*keyFile)
	if err != nil {
		return err
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	fmt.Println(update.FormatPublicKey(pub))
	return nil
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	keyFile := fs.String("key", "", "private key file (default: the "+envKey+" environment variable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	files := fs.Args()
	if len(files) == 0 {
		return errors.New("sign: no files given")
	}
	priv, err := loadKey(*keyFile)
	if err != nil {
		return err
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	for _, f := range files {
		if strings.HasSuffix(f, update.SignatureExt) || strings.HasSuffix(f, ".sha256") {
			continue // let callers pass dist/* without signing the sidecars
		}
		st, err := os.Stat(f)
		if err != nil {
			return err
		}
		if st.IsDir() {
			continue
		}
		out, err := update.SignFile(priv, f)
		if err != nil {
			return err
		}
		fmt.Printf("signed %s -> %s (key %s)\n", f, out, update.KeyID(pub))
	}
	return nil
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	pubs := fs.String("pub", "", "trusted public key(s), ed25519:<base64>, comma-separated (default: the keys pinned in this build)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	files := fs.Args()
	if len(files) == 0 {
		return errors.New("verify: no files given")
	}
	keys := update.TrustedKeys()
	if strings.TrimSpace(*pubs) != "" {
		k, err := update.ParseKeys(*pubs)
		if err != nil {
			return err
		}
		keys = k
	}
	if len(keys) == 0 {
		return errors.New("no public key given and this build pins none; pass -pub ed25519:...")
	}
	for _, f := range files {
		if err := update.VerifyFile(f, keys); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		fmt.Printf("ok %s\n", f)
	}
	return nil
}

// loadKey reads the private key from a file, or from the environment when no
// file is given, so CI can pass a repository secret without touching disk.
func loadKey(file string) (ed25519.PrivateKey, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		return update.ParsePrivateKey(string(b))
	}
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return update.ParsePrivateKey(v)
	}
	return nil, fmt.Errorf("no signing key: pass -key <file> or set %s (base64 32-byte seed)", envKey)
}
