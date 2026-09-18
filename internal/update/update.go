// Package update checks GitHub releases of the project for a newer version
// and can replace the running executable with the downloaded asset.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Client talks to the GitHub API.
type Client struct {
	// APIBase is https://api.github.com by default (tests point it at a fake).
	APIBase string
	HTTP    *http.Client
	GOOS    string
	GOARCH  string
}

func (c *Client) base() string {
	if c.APIBase != "" {
		return strings.TrimRight(c.APIBase, "/")
	}
	return "https://api.github.com"
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (c *Client) goos() string {
	if c.GOOS != "" {
		return c.GOOS
	}
	return runtime.GOOS
}

func (c *Client) goarch() string {
	if c.GOARCH != "" {
		return c.GOARCH
	}
	return runtime.GOARCH
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ValidRepo reports whether repo looks like "owner/name".
func ValidRepo(repo string) bool { return repoPattern.MatchString(repo) }

// Check fetches the latest release of repo and compares it with current.
func (c *Client) Check(ctx context.Context, repo, current string) (model.UpdateInfo, error) {
	info := model.UpdateInfo{Repo: repo, CurrentVersion: current, CheckedAt: time.Now(), CurrentIsDev: IsDev(current)}
	if !ValidRepo(repo) {
		return info, fmt.Errorf("invalid repository %q (expected owner/name)", repo)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.base()+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "GWatch-updater")
	resp, err := c.http().Do(req)
	if err != nil {
		return info, fmt.Errorf("contact GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return info, fmt.Errorf("no releases found for %s (publish a release with a tag like v1.2.0)", repo)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return info, fmt.Errorf("GitHub answered %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return info, fmt.Errorf("decode release: %w", err)
	}
	info.LatestVersion = strings.TrimPrefix(rel.TagName, "v")
	info.ReleaseURL = rel.HTMLURL
	info.ReleaseNotes = rel.Body
	if !rel.PublishedAt.IsZero() {
		t := rel.PublishedAt
		info.PublishedAt = &t
	}
	if a := pickAsset(rel.Assets, c.goos(), c.goarch()); a != nil {
		info.AssetName, info.AssetURL, info.AssetSize = a.Name, a.BrowserDownloadURL, a.Size
	}
	info.UpdateAvailable = info.CurrentIsDev || CompareVersions(info.LatestVersion, current) > 0
	if !c.SigningEnabled() {
		// Checking still works so the user learns a new version exists, but
		// installing it will be refused: say so here rather than at the end of
		// a download.
		info.Error = ErrNoSigningKey.Error() + "; this release can only be installed by hand from " + info.ReleaseURL
	}
	return info, nil
}

// IsDev reports whether the version string is a development/CI build rather
// than a release number.
func IsDev(v string) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" || v == "dev" || strings.HasPrefix(v, "ci-") {
		return true
	}
	return !regexp.MustCompile(`^\d+(\.\d+)*`).MatchString(v)
}

// CompareVersions compares dotted version numbers ("1.2.3" vs "1.10"); a
// pre-release suffix ("-rc1") sorts before the plain version. Returns -1, 0 or 1.
func CompareVersions(a, b string) int {
	na, sa := splitVersion(a)
	nb, sb := splitVersion(b)
	for i := 0; i < len(na) || i < len(nb); i++ {
		var x, y int
		if i < len(na) {
			x = na[i]
		}
		if i < len(nb) {
			y = nb[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	switch {
	case sa == sb:
		return 0
	case sa == "":
		return 1
	case sb == "":
		return -1
	case sa > sb:
		return 1
	}
	return -1
}

func splitVersion(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	suffix := ""
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		suffix = v[i+1:]
		v = v[:i]
	}
	var nums []int
	for _, p := range strings.Split(v, ".") {
		n, _ := strconv.Atoi(p)
		nums = append(nums, n)
	}
	return nums, suffix
}

// pickAsset finds the release asset built for this platform. Assets are
// expected to be named gwatch-<goos>-<goarch>[.exe] (what CI publishes),
// but a few common variants are accepted.
func pickAsset(assets []ghAsset, goos, goarch string) *ghAsset {
	want := []string{
		fmt.Sprintf("gwatch-%s-%s", goos, goarch),
		fmt.Sprintf("gwatch_%s_%s", goos, goarch),
		fmt.Sprintf("gwatch-%s_%s", goos, goarch),
	}
	for _, a := range assets {
		name := strings.ToLower(a.Name)
		if strings.HasSuffix(name, ".sha256") || strings.HasSuffix(name, SignatureExt) || strings.HasSuffix(name, ".txt") || strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		for _, w := range want {
			if name == w || name == w+".exe" {
				aa := a
				return &aa
			}
		}
	}
	// Windows-only release with a single exe.
	if goos == "windows" {
		for _, a := range assets {
			if strings.EqualFold(a.Name, "gwatch.exe") {
				aa := a
				return &aa
			}
		}
	}
	return nil
}

// ErrNoAsset is returned when the release has no binary for this platform.
var ErrNoAsset = errors.New("the release has no executable for this platform")

// Download fetches the asset into dir and verifies its ed25519 signature
// against the release signing keys pinned into this build. Verification is
// mandatory: there is no path on which this returns a file that was not signed
// by a pinned key. The sibling <name>.sha256 asset is still checked when the
// release publishes one, as a cheap guard against transit corruption, but it
// never stands in for the signature. It returns the downloaded path.
func (c *Client) Download(ctx context.Context, info model.UpdateInfo, dir string) (string, error) {
	if info.AssetURL == "" {
		return "", ErrNoAsset
	}
	keys := TrustedKeys()
	if len(keys) == 0 {
		return "", ErrNoSigningKey
	}
	req, err := http.NewRequestWithContext(ctx, "GET", info.AssetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "GWatch-updater")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "gwatch-update-*")
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), resp.Body)
	tmp.Close()
	if err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("download: %w", err)
	}
	if n == 0 || (info.AssetSize > 0 && n != info.AssetSize) {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("download incomplete: got %d bytes, expected %d", n, info.AssetSize)
	}
	digest := hash.Sum(nil)
	if sum, err := c.fetchChecksum(ctx, info.AssetURL); err == nil && sum != "" {
		if got := hex.EncodeToString(digest); !strings.EqualFold(got, sum) {
			os.Remove(tmp.Name())
			return "", fmt.Errorf("checksum mismatch: downloaded file is %s, release says %s", got, sum)
		}
	}
	sigText, err := c.fetchSignature(ctx, info.AssetURL)
	if err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("%w: %s has no %s asset, so it cannot be verified", ErrUnsigned, info.AssetName, SignatureExt)
	}
	if err := VerifyDigest(digest, sigText, keys); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("%w: %s was not signed by a release key this build trusts (%s)", err, info.AssetName, strings.Join(KeyIDs(), ", "))
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// fetchSignature downloads the <asset>.sig file published next to the asset.
func (c *Client) fetchSignature(ctx context.Context, assetURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", assetURL+SignatureExt, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "GWatch-updater")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ErrUnsigned
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	if len(b) == 0 {
		return "", ErrUnsigned
	}
	return string(b), nil
}

func (c *Client) fetchChecksum(ctx context.Context, assetURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", assetURL+".sha256", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "GWatch-updater")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("no checksum")
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	fields := strings.Fields(string(b))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return "", errors.New("bad checksum file")
	}
	return fields[0], nil
}

// Swap replaces exe with newPath: the running executable is renamed to
// exe.old (Windows allows renaming a running program but not overwriting it)
// and the new file moved into place. The previous version is kept as .old.
func Swap(exe, newPath string) error {
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("move current executable aside: %w", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		// Try to put the old one back.
		_ = os.Rename(old, exe)
		return fmt.Errorf("install new executable: %w", err)
	}
	return nil
}

// Executable returns the path of the running program with symlinks resolved.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// DirWritable reports whether files can be created next to path.
func DirWritable(path string) bool {
	f, err := os.CreateTemp(filepath.Dir(path), ".gwatch-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}
