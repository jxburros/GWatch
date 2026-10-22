// Command gwatch-sign generates the GWatch release signing key and signs
// release assets with it. The signature format and all the crypto live in
// internal/update, so the running program verifies with exactly the code that
// signed.
//
// Usage:
//
//	gwatch-sign keygen -out release.key      generate a key pair
//	gwatch-sign pubkey -key release.key      print the ed25519:... line
//	gwatch-sign sign -version V FILE…        write FILE.sig for each file
//	gwatch-sign verify -version V FILE…      check FILE against FILE.sig
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
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jxburros/GWatch/internal/update"
)

const envKey = "GWATCH_SIGNING_KEY"

const usage = `gwatch-sign — sign GWatch release assets

  gwatch-sign keygen -out release.key        generate a signing key pair
  gwatch-sign pubkey [-key release.key]      print the ed25519:... line for release_keys.txt
  gwatch-sign sign -version V [-key release.key] FILE…
                                             write FILE` + update.SignatureExt + ` next to each FILE
  gwatch-sign verify -version V [-pub ed25519:...] FILE…
                                             verify FILE against FILE` + update.SignatureExt + `

-version is the release the files are published in (1.2.3, no tag prefix).
Each signature names its file and that version, and an updater refuses one
offered under any other name or version.

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
	if st, err := os.Lstat(*out); err == nil {
		if !*force {
			return fmt.Errorf("%s already exists; refusing to overwrite a signing key (use -force if you really mean it)", *out)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file (a link or a device?); refusing to write a signing key through it", *out)
		}
	}
	pub, priv, err := update.GenerateKey()
	if err != nil {
		return err
	}
	body := "# GWatch release signing key — keep this file secret and offline.\n" +
		"# Public key: " + update.FormatPublicKey(pub) + "\n" +
		update.EncodeSeed(priv) + "\n"
	if err := writePrivate(*out, []byte(body)); err != nil {
		return err
	}
	fmt.Printf("wrote %s (mode 0600) — keep it offline, it is the only thing that can sign a release\n", *out)
	fmt.Printf("key id: %s\n", update.KeyID(pub))
	fmt.Printf("\nPaste this line into internal/update/release_keys.txt:\n\n%s\n", update.FormatPublicKey(pub))
	fmt.Printf("\nAnd store this value as the %s repository secret:\n\n%s\n", envKey, update.EncodeSeed(priv))
	return nil
}

// writePrivate writes a secret file that only its owner can read. The bytes go
// to a new 0600 file beside the destination, which is then renamed over it:
// os.WriteFile's mode applies only when it creates a file, so overwriting an
// existing, more permissive one in place would keep that file's mode.
func writePrivate(path string, body []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gwatch-sign-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op once renamed
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		if st.Mode().Perm() != 0o600 {
			return fmt.Errorf("%s ended up with mode %v, not 0600; fix its permissions before using it", path, st.Mode().Perm())
		}
	}
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
	version := fs.String("version", "", "the release version the files are published in, e.g. 1.2.3 (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	files := fs.Args()
	if len(files) == 0 {
		return errors.New("sign: no files given")
	}
	if strings.TrimSpace(*version) == "" {
		return errors.New("sign: -version is required: each signature names the release it belongs to")
	}
	priv, err := loadKey(*keyFile)
	if err != nil {
		return err
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	signed := 0
	for _, f := range files {
		if skipSidecar(f) {
			continue // let callers pass dist/* without signing the sidecars
		}
		st, err := os.Stat(f)
		if err != nil {
			return err
		}
		if st.IsDir() {
			continue
		}
		out, err := update.SignFile(priv, f, strings.TrimSpace(*version))
		if err != nil {
			return err
		}
		fmt.Printf("signed %s -> %s (key %s)\n", f, out, update.KeyID(pub))
		signed++
	}
	if signed == 0 {
		return errors.New("sign: nothing to sign (only signature or checksum files were given)")
	}
	return nil
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	pubs := fs.String("pub", "", "trusted public key(s), ed25519:<base64>, comma-separated (default: the keys pinned in this build)")
	version := fs.String("version", "", "the release version the files are published in, e.g. 1.2.3 (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	files := fs.Args()
	if len(files) == 0 {
		return errors.New("verify: no files given")
	}
	if strings.TrimSpace(*version) == "" {
		return errors.New("verify: -version is required: a signature is only valid for the release it names")
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
		return errors.New("no public key given and this build pins none; pass -pub with an ed25519 public key")
	}
	checked := 0
	for _, f := range files {
		if skipSidecar(f) {
			continue
		}
		if err := update.VerifyFile(f, strings.TrimSpace(*version), keys); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		fmt.Printf("ok %s\n", f)
		checked++
	}
	if checked == 0 {
		return errors.New("verify: nothing to check (only signature or checksum files were given)")
	}
	return nil
}

// skipSidecar reports whether a path is one of the files published next to an
// asset, so callers can pass a whole dist/* glob.
func skipSidecar(path string) bool {
	return strings.HasSuffix(path, update.SignatureExt) || strings.HasSuffix(path, ".sha256")
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
