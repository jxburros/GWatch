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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Client talks to the GitHub API.
//
// One repository publishes more than one thing: GWatch itself, and the
// hardware agent, which versions and releases on its own (docs/RELEASING.md).
// AssetPrefix and TagPrefix are what keep the two apart, so an agent asking
// what it should move to is never offered a server build, and the other way
// round. Left empty they describe GWatch, which is what every existing caller
// means.
type Client struct {
	// APIBase is https://api.github.com by default (tests point it at a fake).
	APIBase string
	HTTP    *http.Client
	GOOS    string
	GOARCH  string
	// AssetPrefix is the first part of the asset names this client installs:
	// "gwatch" (the default) matches gwatch-<goos>-<goarch>[.exe],
	// "gwatch-agent" matches gwatch-agent-<goos>-<goarch>[.exe].
	AssetPrefix string
	// TagPrefix is what a release tag must start with to belong to this
	// family, and what is stripped to get the version. Empty means the
	// project's own tags — v1.2.3, or a bare 1.2.3 — and excludes every tag
	// carrying a family prefix of its own, such as agent-v1.2.3.
	TagPrefix string
}

// AgentAssetPrefix and AgentTagPrefix describe the hardware agent's releases.
// They are here rather than in the agent so that GWatch can ask what the
// newest agent is — to tell you which machines are behind — without owning a
// copy of the naming.
const (
	AgentAssetPrefix = "gwatch-agent"
	AgentTagPrefix   = "agent-v"
)

// ForAgent returns a copy of this client that speaks about the agent's
// releases instead of GWatch's.
func (c *Client) ForAgent() *Client {
	cp := *c
	cp.AssetPrefix, cp.TagPrefix = AgentAssetPrefix, AgentTagPrefix
	return &cp
}

func (c *Client) assetPrefix() string {
	if c.AssetPrefix != "" {
		return c.AssetPrefix
	}
	return "gwatch"
}

// releaseVersion reports the version a tag carries, and whether the tag
// belongs to this client's family at all.
//
// With a TagPrefix the test is simply whether the tag starts with it. Without
// one the tag is GWatch's own, which means it is a version possibly preceded
// by "v" — and, importantly, that a tag belonging to some other family
// (agent-v0.4.0, mcp/v0.1.0) is not ours, however new it is.
func (c *Client) releaseVersion(tag string) (string, bool) {
	tag = strings.TrimSpace(tag)
	if c.TagPrefix != "" {
		if !strings.HasPrefix(tag, c.TagPrefix) {
			return "", false
		}
		tag = strings.TrimPrefix(tag, c.TagPrefix)
	}
	v := strings.TrimPrefix(tag, "v")
	if v == "" || v[0] < '0' || v[0] > '9' {
		return "", false
	}
	return v, true
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

// get fetches a GitHub API path and decodes it into out.
func (c *Client) get(ctx context.Context, repo, path string, out any) error {
	if !ValidRepo(repo) {
		return fmt.Errorf("invalid repository %q (expected owner/name)", repo)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.base()+"/repos/"+repo+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "GWatch-updater")
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("contact GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("no releases found for %s (publish a release with a tag like v1.2.0)", repo)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("GitHub answered %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode release: %w", err)
	}
	return nil
}

// CatalogSize is how many releases back Releases looks. A release every
// fortnight keeps rather more than a year of them in view, which is as far
// back as anyone sensibly installs.
const CatalogSize = 50

// Releases lists the repository's releases, newest first, each annotated for
// this build: whether it carries an executable for this platform and how it
// compares with the running version. Drafts are left out — they are not public
// — but pre-releases are included and flagged, so the caller decides whether
// to offer them.
func (c *Client) Releases(ctx context.Context, repo, current string) ([]model.Release, error) {
	var rels []ghRelease
	if err := c.get(ctx, repo, fmt.Sprintf("/releases?per_page=%d", CatalogSize), &rels); err != nil {
		return nil, err
	}
	out := make([]model.Release, 0, len(rels))
	for _, rel := range rels {
		if rel.Draft {
			continue
		}
		version, ok := c.releaseVersion(rel.TagName)
		if !ok {
			continue
		}
		r := model.Release{
			Version:    version,
			Tag:        rel.TagName,
			Name:       rel.Name,
			Prerelease: rel.Prerelease,
			Notes:      rel.Body,
			URL:        rel.HTMLURL,
		}
		if !rel.PublishedAt.IsZero() {
			t := rel.PublishedAt
			r.PublishedAt = &t
		}
		if a := pickAsset(rel.Assets, c.assetPrefix(), c.goos(), c.goarch()); a != nil {
			r.AssetName, r.AssetURL, r.AssetSize, r.Installable = a.Name, a.BrowserDownloadURL, a.Size, true
		}
		cmp := CompareVersions(r.Version, current)
		r.Newer = IsDev(current) || cmp > 0
		r.Running = !IsDev(current) && cmp == 0
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return CompareVersions(out[i].Version, out[j].Version) > 0 })
	return out, nil
}

// Newest returns the release a build on current should move to, or nil when
// there is nothing newer. Pre-releases are skipped unless includePrerelease.
//
// A release with no executable for this platform is still returned: learning
// that a new version exists is worth having even where GWatch cannot install
// it for you, and installing is where that is refused, with an explanation.
func Newest(rels []model.Release, includePrerelease bool) *model.Release {
	for i := range rels {
		r := rels[i]
		if !r.Newer {
			continue
		}
		if r.Prerelease && !includePrerelease {
			continue
		}
		return &rels[i]
	}
	return nil
}

// Find returns the release with the given version, or nil. The version is
// matched with or without its leading "v" so a tag and a version string are
// both accepted.
func Find(rels []model.Release, version string) *model.Release {
	want := strings.TrimPrefix(strings.TrimSpace(version), "v")
	for i := range rels {
		if rels[i].Version == want {
			return &rels[i]
		}
	}
	return nil
}

// infoFor renders a release as the UpdateInfo the API and UI speak in.
func (c *Client) infoFor(repo, current string, rel *model.Release) model.UpdateInfo {
	info := model.UpdateInfo{Repo: repo, CurrentVersion: current, CheckedAt: time.Now(), CurrentIsDev: IsDev(current)}
	if rel == nil {
		info.LatestVersion = current
		return info
	}
	info.LatestVersion = rel.Version
	info.ReleaseURL = rel.URL
	info.ReleaseNotes = rel.Notes
	info.PublishedAt = rel.PublishedAt
	info.AssetName, info.AssetURL, info.AssetSize = rel.AssetName, rel.AssetURL, rel.AssetSize
	info.Prerelease = rel.Prerelease
	info.UpdateAvailable = rel.Newer
	return info
}

// Check reports the newest release this build could move to. With
// includePrerelease it considers pre-releases too, at the caller's risk.
//
// Checking works even in a build with no pinned signing key, so the user still
// learns that a new version exists; installing it is what gets refused, and
// the refusal is put in Error here rather than at the end of a download.
func (c *Client) Check(ctx context.Context, repo, current string, includePrerelease bool) (model.UpdateInfo, error) {
	rels, err := c.Releases(ctx, repo, current)
	if err != nil {
		return model.UpdateInfo{Repo: repo, CurrentVersion: current, CheckedAt: time.Now(), CurrentIsDev: IsDev(current), LatestVersion: current}, err
	}
	newest := Newest(rels, includePrerelease)
	if newest == nil {
		// Nothing to move to: report the newest release that exists so the
		// interface can say which version "up to date" means.
		for i := range rels {
			if rels[i].Prerelease && !includePrerelease {
				continue
			}
			newest = &rels[i]
			break
		}
		info := c.infoFor(repo, current, newest)
		info.UpdateAvailable = false
		return info, nil
	}
	info := c.infoFor(repo, current, newest)
	if !c.SigningEnabled() {
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
// expected to be named <prefix>-<goos>-<goarch>[.exe] (what CI publishes),
// but a few common variants are accepted.
//
// The match is exact, which is what keeps the families apart in a release
// that carries several: asked for "gwatch", it will not take
// gwatch-agent-linux-amd64 or gwatch-mcp-linux-amd64.
func pickAsset(assets []ghAsset, prefix, goos, goarch string) *ghAsset {
	want := []string{
		fmt.Sprintf("%s-%s-%s", prefix, goos, goarch),
		fmt.Sprintf("%s_%s_%s", prefix, goos, goarch),
		fmt.Sprintf("%s-%s_%s", prefix, goos, goarch),
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
			if strings.EqualFold(a.Name, prefix+".exe") {
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
