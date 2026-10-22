package backup

import (
	"archive/zip"
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// MaxArchiveBytes is the largest encrypted archive Open accepts, and so the
// largest upload the restore endpoint takes. A full backup is compressed
// JSON; two gigabytes of it is many months of raw results for hundreds of
// checks, well past the default retention. The decrypted ZIP is never
// larger than the encrypted file, so the same figure bounds the temporary
// copy Open writes.
const MaxArchiveBytes int64 = 2 << 30

// ErrArchiveTooLarge is returned when an archive, or something inside it,
// exceeds one of the restore limits. Nothing has been written to the
// database when a restore fails with it.
var ErrArchiveTooLarge = errors.New("backup archive exceeds the restore size limits")

// Limits applied to the contents of an archive while it is read. A real
// archive has at most six entries and a config.json of a few megabytes; the
// figures leave room for very large installations while keeping a crafted
// archive (a ZIP bomb, a history of billions of rows) from exhausting
// memory, disk or the database. Variables rather than constants so tests
// can lower them.
var (
	maxArchiveBytes        = MaxArchiveBytes
	maxEntries             = 64
	maxManifestBytes int64 = 1 << 20
	maxConfigBytes   int64 = 256 << 20
	// maxHistoryBytes is the decompressed budget shared by every history
	// file in one archive.
	maxHistoryBytes int64 = 32 << 30
	// maxHistoryRecords caps each of the results, rollups and events counts
	// a manifest may claim.
	maxHistoryRecords = 200_000_000
)

// historyFiles are the JSON-lines entries a restore imports, in order.
var historyFiles = []string{"results.jsonl", "rollups.jsonl", "events.jsonl"}

// capReader reads at most n bytes from r and fails with ErrArchiveTooLarge,
// not io.EOF, when the source holds more, so a truncated document is never
// mistaken for a complete one.
type capReader struct {
	r io.Reader
	n int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.n <= 0 {
		// Probe one byte so a source of exactly n bytes still ends cleanly.
		var one [1]byte
		k, err := c.r.Read(one[:])
		if k > 0 {
			return 0, ErrArchiveTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > c.n {
		p = p[:c.n]
	}
	k, err := c.r.Read(p)
	c.n -= int64(k)
	return k, err
}

// checkEntries rejects an archive whose directory is out of bounds before any
// entry is decompressed: too many entries, a duplicated name (readers take
// the first match, so a second copy could hide from one pass and not
// another), or declared sizes past the limits. The declared sizes are
// attacker-chosen, so every read is limited again as it happens.
func checkEntries(zr *zip.Reader) error {
	if len(zr.File) > maxEntries {
		return fmt.Errorf("%w: %d entries", ErrArchiveTooLarge, len(zr.File))
	}
	seen := map[string]bool{}
	var history uint64
	for _, f := range zr.File {
		if seen[f.Name] {
			return fmt.Errorf("backup archive has %s more than once", f.Name)
		}
		seen[f.Name] = true
		switch f.Name {
		case "manifest.json":
			if f.UncompressedSize64 > uint64(maxManifestBytes) {
				return fmt.Errorf("%w: manifest.json", ErrArchiveTooLarge)
			}
		case "config.json":
			if f.UncompressedSize64 > uint64(maxConfigBytes) {
				return fmt.Errorf("%w: config.json", ErrArchiveTooLarge)
			}
		default:
			history += f.UncompressedSize64
			if history > uint64(maxHistoryBytes) {
				return fmt.Errorf("%w: history", ErrArchiveTooLarge)
			}
		}
	}
	return nil
}

// checkManifest bounds the record counts a manifest claims. The history
// import holds each file to its claim, so the claims bound the database
// growth a restore can cause.
func checkManifest(m Manifest) error {
	for name, n := range map[string]int{"results": m.Results, "rollups": m.Rollups, "events": m.Events} {
		if n < 0 || n > maxHistoryRecords {
			return fmt.Errorf("%w: manifest claims %d %s", ErrArchiveTooLarge, n, name)
		}
	}
	return nil
}

// claimed is the record count the manifest gives for a history file.
func claimed(m Manifest, name string) int {
	switch name {
	case "results.jsonl":
		return m.Results
	case "rollups.jsonl":
		return m.Rollups
	case "events.jsonl":
		return m.Events
	}
	return 0
}

// preflightHistory reads every history file once, decompressing through the
// shared byte budget and counting records, so an archive over a limit (or
// holding more rows than its manifest says) fails before the restore
// touches the database. The import pass enforces the same limits again.
func preflightHistory(zr *zip.Reader, m Manifest) error {
	budget := maxHistoryBytes
	for _, name := range historyFiles {
		if err := scanLines(zr, name, &budget, claimed(m, name), func([]byte) error { return nil }); err != nil {
			return err
		}
	}
	return nil
}

// scanLines calls fn with each non-blank line of entry name, reading at most
// *budget decompressed bytes (and charging what it reads to the budget) and
// at most maxRecords lines. A missing entry is not an error.
func scanLines(zr *zip.Reader, name string, budget *int64, maxRecords int, fn func([]byte) error) error {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		cr := &capReader{r: rc, n: *budget}
		defer func() { *budget = cr.n }()
		sc := bufio.NewScanner(cr)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		records := 0
		for sc.Scan() {
			if len(bytes.TrimSpace(sc.Bytes())) == 0 {
				continue
			}
			records++
			if records > maxRecords {
				return fmt.Errorf("%w: %s has more records than its manifest claims", ErrArchiveTooLarge, name)
			}
			if err := fn(sc.Bytes()); err != nil {
				return err
			}
		}
		return sc.Err()
	}
	return nil
}
