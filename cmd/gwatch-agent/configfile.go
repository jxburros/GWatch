// The agent's configuration file.
//
// Everything about how this machine talks to GWatch lives in one plain-text
// JSON file, agent.json, that a person can open and edit: where the server is,
// what this machine calls itself, how often it reports, the token it holds, and
// which readings it is willing to send. Changes take effect when the agent is
// restarted. Before this file existed those settings were baked into the
// service's command line at install time, and changing one meant reinstalling
// the agent.
//
// Precedence, lowest to highest: built-in defaults, this file, environment
// variables, command-line flags. `gwatch-agent config show` prints every
// setting with the layer it came from, which is the answer to "I edited the file
// and nothing changed".
//
// The file holds the token, so it is created readable only by the account that
// owns it (and, on Windows, SYSTEM and the administrators). The agent rewrites
// it in one case — taking up a replacement token during a rotation — and does
// so by changing only the token and leaving everything else, including keys it
// does not know, as it found them.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/permissions"
)

const configFileName = "agent.json"

const (
	defaultCheckIn = model.CheckInDefault
	// defaultBuffer is how long readings are held while GWatch cannot be
	// reached, so that an outage on the network or the server leaves a gap in
	// the history shorter than the outage rather than the whole of it.
	defaultBuffer = 6 * time.Hour
	maxBuffer     = 48 * time.Hour
)

// configPathOverride is --config, once main has read it.
var configPathOverride string

// configFile is where the configuration lives: --config, then
// GWATCH_AGENT_CONFIG, then beside the token file in the agent's data
// directory — the same place GWatch's own files are looked for.
func configFile() string {
	if configPathOverride != "" {
		return configPathOverride
	}
	if p := strings.TrimSpace(os.Getenv("GWATCH_AGENT_CONFIG")); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(tokenFile()), configFileName)
}

// configKeys is every setting the file knows, in the order they are written.
var configKeys = []string{
	"server", "token", "name", "interval", "checkIn", "buffer", "autoUpdate",
	"insecure", "certPin", "listen", "repo", "hostRoot", "collect", "collector",
}

// configDoc is the file as it is on disk. The settings are read from it, and a
// change is made to it rather than to a fresh rendering of the settings, which
// is what lets a key the agent has never heard of survive a rewrite.
type configDoc struct {
	path   string
	exists bool
	// recovered is set when the file itself was damaged — emptied or left
	// half-written by a power cut — and the copy kept beside it was read
	// instead. The next save writes the file whole again.
	recovered bool
	raw       map[string]json.RawMessage
}

// loadConfigDoc reads the file. A file that is not there is an empty document,
// not an error; one that cannot be parsed is an error that names the file.
//
// The one exception is a file that a power cut has damaged: emptied, filled
// with zeros, or cut off part-way. Every save also keeps a copy beside the
// file (backupPath), and when the file is damaged and that copy is whole, the
// copy is what is read. Without it a machine that lost power at the wrong
// moment would come back refusing to start, and nobody would visit it to find
// out why.
func loadConfigDoc(path string) (*configDoc, error) {
	d := &configDoc{path: path, raw: map[string]json.RawMessage{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	d.exists = true
	if looksDamaged(b) {
		if raw, ok := readBackup(path); ok {
			d.raw, d.recovered = raw, true
			return d, nil
		}
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return d, nil
	}
	if err := json.Unmarshal(b, &d.raw); err != nil {
		var syn *json.SyntaxError
		if errors.Is(err, io.ErrUnexpectedEOF) || (errors.As(err, &syn) && int(syn.Offset) >= len(bytes.TrimSpace(b))) {
			// Cut off part-way: what a write interrupted by a power cut
			// leaves behind.
			if raw, ok := readBackup(path); ok {
				d.raw, d.recovered = raw, true
				return d, nil
			}
		}
		return nil, fmt.Errorf("%s is not valid JSON: %s", path, jsonErrorWhere(b, err))
	}
	if d.raw == nil {
		d.raw = map[string]json.RawMessage{}
	}
	return d, nil
}

// backupPath is the copy of the configuration file every save keeps.
func backupPath(path string) string { return path + ".bak" }

// keepConfigBackup makes sure the copy beside the file matches it, for a file
// written before copies were kept or edited by hand since. It is best effort:
// the copy is a safety net, and a failure to write it stops nothing.
func keepConfigBackup(path string) {
	b, err := os.ReadFile(path)
	if err != nil || looksDamaged(b) {
		return
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil || len(raw) == 0 {
		return
	}
	if cur, err := os.ReadFile(backupPath(path)); err == nil && bytes.Equal(cur, b) {
		return
	}
	if writeFileAtomic(backupPath(path), b) == nil {
		restrictWindowsACL(backupPath(path))
	}
}

// looksDamaged says whether the bytes are what an interrupted write leaves: an
// empty file, or one holding zeros where the text should be (NTFS and ext4 can
// both leave a file its new length with none of its new contents).
func looksDamaged(b []byte) bool {
	return len(bytes.TrimSpace(b)) == 0 || bytes.IndexByte(b, 0) >= 0
}

// readBackup reads the copy kept beside the file, if there is one and it is a
// whole JSON object with something in it.
func readBackup(path string) (map[string]json.RawMessage, bool) {
	b, err := os.ReadFile(backupPath(path))
	if err != nil || looksDamaged(b) {
		return nil, false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil || len(raw) == 0 {
		return nil, false
	}
	return raw, true
}

// jsonErrorWhere words a JSON syntax error with a line number, which is the
// part someone editing by hand needs.
func jsonErrorWhere(b []byte, err error) string {
	var syn *json.SyntaxError
	if errors.As(err, &syn) {
		line := 1 + bytes.Count(b[:min(int(syn.Offset), len(b))], []byte("\n"))
		return fmt.Sprintf("%v (line %d)", err, line)
	}
	return err.Error()
}

func (d *configDoc) has(key string) bool { _, ok := d.raw[key]; return ok }

func (d *configDoc) set(key string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	d.raw[key] = b
}

// unknownKeys lists keys in the file that nothing reads, which is nearly
// always a typo.
func (d *configDoc) unknownKeys() []string {
	known := map[string]bool{}
	for _, k := range configKeys {
		known[k] = true
	}
	var out []string
	for k := range d.raw {
		if !known[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// bytes renders the document: known keys in their usual order, anything else
// after them in alphabetical order.
func (d *configDoc) bytes() []byte {
	var buf bytes.Buffer
	buf.WriteString("{\n")
	keys := make([]string, 0, len(d.raw))
	for _, k := range configKeys {
		if d.has(k) {
			keys = append(keys, k)
		}
	}
	keys = append(keys, d.unknownKeys()...)
	for i, k := range keys {
		var v bytes.Buffer
		if err := json.Indent(&v, d.raw[k], "  ", "  "); err != nil {
			v.Reset()
			v.Write(d.raw[k])
		}
		key, _ := json.Marshal(k)
		fmt.Fprintf(&buf, "  %s: %s", key, v.String())
		if i < len(keys)-1 {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString("}\n")
	return buf.Bytes()
}

// save writes the document so that nobody ever sees half of it: to a sibling
// file, then moved over the original. It is private from the moment it exists.
// A copy is then kept beside it (see loadConfigDoc), written the same way, so
// that whichever of the two writes a power cut interrupts, the other is whole.
func (d *configDoc) save() error {
	dir := filepath.Dir(d.path)
	if err := permissions.EnsurePrivateDir(dir); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	body := d.bytes()
	if err := writeFileAtomic(d.path, body); err != nil {
		return err
	}
	restrictWindowsACL(d.path)
	d.exists, d.recovered = true, false
	if err := writeFileAtomic(backupPath(d.path), body); err == nil {
		restrictWindowsACL(backupPath(d.path))
	}
	return nil
}

// writeFileAtomic replaces path with data, readable only by its owner, so that
// a reader — or a power cut — sees the old file or the new one and never part
// of either: the data goes to a sibling file, is flushed to the disk, and is
// then renamed over the original, and the directory is flushed so the rename
// itself survives.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Chmod(name, 0o600); err != nil && !isWindows() {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// syncDir flushes a directory, which is what makes a rename durable on Linux
// and macOS. Windows has no such call for a directory and needs none: NTFS
// journals the rename.
func syncDir(dir string) {
	if isWindows() {
		return
	}
	if f, err := os.Open(dir); err == nil {
		_ = f.Sync()
		f.Close()
	}
}

func isWindows() bool { return filepath.Separator == '\\' }

// ---- the typed view ----

// fileSettings is the file's settings, with "not in the file" distinguishable
// from "set to the zero value" for the ones where it matters.
type fileSettings struct {
	Server, Token, Name, Listen, CertPin, Repo, HostRoot string
	Interval, CheckIn, Buffer                            time.Duration
	AutoUpdate, Insecure, Collector                      *bool
	Collect                                              model.CollectSelection
}

// settings reads the typed view and reports every problem it finds, so a
// person fixing their file is told all of it at once.
func (d *configDoc) settings() (fileSettings, []string) {
	var fsx fileSettings
	var problems []string
	bad := func(key, why string) { problems = append(problems, fmt.Sprintf("%s: %s", key, why)) }

	str := func(key string, dst *string) {
		raw, ok := d.raw[key]
		if !ok {
			return
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			bad(key, "must be text, in quotes")
			return
		}
		*dst = strings.TrimSpace(s)
	}
	dur := func(key string, dst *time.Duration) {
		raw, ok := d.raw[key]
		if !ok {
			return
		}
		v, err := parseDurationJSON(raw)
		if err != nil {
			bad(key, err.Error())
			return
		}
		*dst = v
	}
	flag := func(key string, dst **bool) {
		raw, ok := d.raw[key]
		if !ok {
			return
		}
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			bad(key, "must be true or false")
			return
		}
		*dst = &b
	}

	str("server", &fsx.Server)
	str("token", &fsx.Token)
	str("name", &fsx.Name)
	str("listen", &fsx.Listen)
	str("certPin", &fsx.CertPin)
	str("repo", &fsx.Repo)
	str("hostRoot", &fsx.HostRoot)
	dur("interval", &fsx.Interval)
	dur("checkIn", &fsx.CheckIn)
	dur("buffer", &fsx.Buffer)
	flag("autoUpdate", &fsx.AutoUpdate)
	flag("insecure", &fsx.Insecure)
	flag("collector", &fsx.Collector)
	if raw, ok := d.raw["collect"]; ok {
		if err := json.Unmarshal(raw, &fsx.Collect); err != nil {
			bad("collect", "must be an object like {\"interfaces\": [\"eth0\"]}")
		} else {
			fsx.Collect = fsx.Collect.Normalize()
			if err := fsx.Collect.Validate(); err != nil {
				bad("collect", err.Error())
			}
		}
	}

	if _, ok := d.raw["interval"]; ok && fsx.Interval != 0 && fsx.Interval < minInterval {
		bad("interval", fmt.Sprintf("must be at least %s", formatDuration(minInterval)))
	}
	if _, ok := d.raw["checkIn"]; ok && fsx.CheckIn != 0 && (fsx.CheckIn < model.CheckInMin || fsx.CheckIn > model.CheckInMax) {
		bad("checkIn", fmt.Sprintf("must be between %s and %s", formatDuration(model.CheckInMin), formatDuration(model.CheckInMax)))
	}
	if _, ok := d.raw["buffer"]; ok && (fsx.Buffer < 0 || fsx.Buffer > maxBuffer) {
		bad("buffer", fmt.Sprintf("must be between 0 and %s (0 keeps nothing while the server is unreachable)", formatDuration(maxBuffer)))
	}
	return fsx, problems
}

// parseDurationJSON reads a duration written as text ("90s", "15m", "6h") or as
// a bare number of seconds.
func parseDurationJSON(raw json.RawMessage) (time.Duration, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		if d, err := time.ParseDuration(s); err == nil {
			return d, nil
		}
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return time.Duration(n * float64(time.Second)), nil
		}
		return 0, fmt.Errorf("%q is not a duration; write it like \"90s\", \"15m\" or \"6h\"", s)
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return time.Duration(n * float64(time.Second)), nil
	}
	return 0, errors.New("must be a duration like \"90s\", \"15m\" or \"6h\"")
}

// formatDuration writes a duration the way a person would type it into the file.
func formatDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0s"
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}

// ---- layering ----

// Where a setting's value came from, as `config show` reports it.
const (
	srcDefault = "default"
	srcFile    = "config file"
	srcLegacy  = "old token/settings file"
	srcEnv     = "environment"
	srcFlag    = "command line"
)

// defaultConfig is the agent with nothing configured.
func defaultConfig() config {
	c := config{
		interval:   defaultInterval,
		listen:     defaultListen,
		repo:       defaultRepo,
		autoUpdate: true,
		checkIn:    defaultCheckIn,
		buffer:     defaultBuffer,
		sources:    map[string]string{},
	}
	for _, k := range configKeys {
		c.sources[k] = srcDefault
	}
	return c
}

// baseConfig builds the configuration from everything but the command line:
// defaults, then the file (or, failing that, what an older agent left behind),
// then the environment. The command line is applied by main on top of it, as
// flag defaults.
//
// problems are things wrong with the file; the agent refuses to run on them,
// since silently ignoring a setting someone took the trouble to write is worse
// than stopping. warnings are things worth saying that do not stop it.
func baseConfig() (c config, problems, warnings []string) {
	c = defaultConfig()
	c.configPath = configFile()

	doc, err := loadConfigDoc(c.configPath)
	if err != nil {
		return c, []string{err.Error()}, nil
	}
	c.fileExists = doc.exists
	if doc.recovered {
		warnings = append(warnings, fmt.Sprintf("%s was damaged (most likely by a power cut while it was being written); using the copy kept in %s", c.configPath, backupPath(c.configPath)))
	}
	fsx, fileProblems := doc.settings()
	for _, p := range fileProblems {
		problems = append(problems, fmt.Sprintf("%s: %s", c.configPath, p))
	}
	for _, k := range doc.unknownKeys() {
		warnings = append(warnings, fmt.Sprintf("%s: %q is not a setting this agent knows; check the spelling (config show lists them)", c.configPath, k))
	}

	set := func(key, src string, apply func()) { apply(); c.sources[key] = src }
	if doc.has("server") {
		set("server", srcFile, func() { c.server = fsx.Server })
	}
	if doc.has("token") {
		set("token", srcFile, func() { c.token = fsx.Token })
	}
	if doc.has("name") {
		set("name", srcFile, func() { c.name = fsx.Name })
	}
	if fsx.Interval != 0 {
		set("interval", srcFile, func() { c.interval = fsx.Interval })
	}
	if fsx.CheckIn != 0 {
		set("checkIn", srcFile, func() { c.checkIn = fsx.CheckIn })
	}
	if doc.has("buffer") {
		set("buffer", srcFile, func() { c.buffer = fsx.Buffer })
	}
	if fsx.AutoUpdate != nil {
		set("autoUpdate", srcFile, func() { c.autoUpdate = *fsx.AutoUpdate })
	}
	if fsx.Insecure != nil {
		set("insecure", srcFile, func() { c.insecure = *fsx.Insecure })
	}
	if doc.has("certPin") {
		set("certPin", srcFile, func() { c.certPin = fsx.CertPin })
	}
	if fsx.Listen != "" {
		set("listen", srcFile, func() { c.listen = fsx.Listen })
	}
	if fsx.Repo != "" {
		set("repo", srcFile, func() { c.repo = fsx.Repo })
	}
	if doc.has("hostRoot") {
		set("hostRoot", srcFile, func() { c.hostRoot = fsx.HostRoot })
	}
	if doc.has("collect") {
		set("collect", srcFile, func() { c.collect = fsx.Collect })
	}
	if fsx.Collector != nil {
		set("collector", srcFile, func() { c.collector = *fsx.Collector })
	}
	c.fileServer = c.server

	// An agent paired by an older version kept its token and its server in two
	// small files of their own. They are read only when the new file does not say.
	if c.token == "" {
		if t, err := readLegacyToken(); err == nil {
			c.token, c.sources["token"] = t, srcLegacy
		}
	}
	if c.server == "" {
		if s, err := readLegacySettings(); err == nil {
			c.server, c.certPin, c.insecure = s.Server, s.CertPin, s.Insecure
			c.sources["server"], c.sources["certPin"], c.sources["insecure"] = srcLegacy, srcLegacy, srcLegacy
			if c.name == "" && s.Name != "" {
				c.name, c.sources["name"] = s.Name, srcLegacy
			}
			c.fileServer = c.server
		}
	}

	// The environment overrides the file.
	env := func(key, name string, apply func(v string) bool) {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" && apply(v) {
			c.sources[key] = srcEnv
		}
	}
	env("server", "GWATCH_SERVER", func(v string) bool { c.server = v; return true })
	env("token", "GWATCH_AGENT_TOKEN", func(v string) bool { c.token = v; return true })
	env("interval", "GWATCH_AGENT_INTERVAL", func(v string) bool {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return false
		}
		c.interval = d
		return true
	})
	env("listen", "GWATCH_AGENT_LISTEN", func(v string) bool { c.listen = v; return true })
	env("certPin", "GWATCH_AGENT_CERT_PIN", func(v string) bool { c.certPin = v; return true })
	env("repo", "GWATCH_AGENT_REPO", func(v string) bool { c.repo = v; return true })
	env("hostRoot", "GWATCH_HOST_ROOT", func(v string) bool { c.hostRoot = v; return true })
	if v, ok := envBoolSet("GWATCH_AGENT_AUTO_UPDATE"); ok {
		c.autoUpdate, c.sources["autoUpdate"] = v, srcEnv
	}
	if v, ok := envBoolSet("GWATCH_AGENT_COLLECTOR"); ok {
		c.collector, c.sources["collector"] = v, srcEnv
	}
	c.code = os.Getenv("GWATCH_AGENT_CODE")

	c.tokenSource = c.sources["token"]
	return c, problems, warnings
}

// ---- writing ----

// persistConfig records the settings a person gave when setting a machine up
// (pair, install) in the file. A file that is not there yet is written in full,
// defaults included, so that the editable settings are all visible to whoever
// opens it. A file that is there is changed only where a setting was given
// explicitly now, plus the server and token, which pairing always sets.
func persistConfig(c config) error {
	doc, err := loadConfigDoc(c.configPathOrDefault())
	if err != nil {
		return err
	}
	fresh := !doc.exists || len(doc.raw) == 0
	explicit := func(key string) bool { return c.sources[key] == srcFlag || c.sources[key] == srcEnv }
	put := func(key string, v any, always bool) {
		if fresh || always || explicit(key) {
			doc.set(key, v)
		}
	}

	if c.server != "" {
		doc.set("server", c.baseURL())
	}
	if c.token != "" {
		doc.set("token", c.token)
	}
	put("name", c.name, false)
	put("interval", formatDuration(c.interval), false)
	put("checkIn", formatDuration(c.checkIn), false)
	put("buffer", formatDuration(c.buffer), false)
	put("autoUpdate", c.autoUpdate, false)
	if fresh || explicit("insecure") || c.insecure {
		doc.set("insecure", c.insecure)
	}
	if c.certPin != "" || doc.has("certPin") {
		doc.set("certPin", c.certPin)
	}
	// Serve mode's address is only written when someone gave it or it is already
	// there; a push-mode agent has no use for it and the file is not the place
	// to advertise it.
	if explicit("listen") || doc.has("listen") {
		doc.set("listen", c.listen)
	}
	if c.repo != defaultRepo || doc.has("repo") {
		doc.set("repo", c.repo)
	}
	if c.hostRoot != "" || doc.has("hostRoot") {
		doc.set("hostRoot", c.hostRoot)
	}
	if fresh || explicit("collect") || !c.collect.IsZero() {
		coll := c.collect.Normalize()
		doc.set("collect", coll)
	}
	// Written false into a new file so that whoever opens it sees the switch
	// is there; never turned on by anything but a person (collector.go).
	if fresh || explicit("collector") || doc.has("collector") {
		doc.set("collector", c.collector)
	}
	return doc.save()
}

func (c config) configPathOrDefault() string {
	if c.configPath != "" {
		return c.configPath
	}
	return configFile()
}

// storeToken replaces the token in the file and leaves everything else alone.
// It is how a rotation is taken up.
func storeToken(path, token string) error {
	doc, err := loadConfigDoc(path)
	if err != nil {
		return err
	}
	doc.set("token", token)
	if err := doc.save(); err != nil {
		return err
	}
	// An older agent that this one replaced (or that a rollback restores) reads
	// the token from its own file; keep that one in step so a rollback after a
	// rotation does not strand the machine.
	if _, err := os.Stat(tokenFile()); err == nil {
		_, _ = saveLegacyToken(token)
	}
	return nil
}

// ---- what older agents left behind ----

// legacySettings is what pairing used to keep beside the token.
type legacySettings struct {
	CertPin  string `json:"certPin,omitempty"`
	Server   string `json:"server"`
	Insecure bool   `json:"insecure,omitempty"`
	Name     string `json:"name,omitempty"`
}

func legacySettingsFile() string {
	return filepath.Join(filepath.Dir(tokenFile()), "agent-settings.json")
}

func readLegacySettings() (legacySettings, error) {
	var s legacySettings
	b, err := os.ReadFile(legacySettingsFile())
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("%s: %w", legacySettingsFile(), err)
	}
	if strings.TrimSpace(s.Server) == "" {
		return s, errors.New("the stored settings name no server")
	}
	return s, nil
}

func readLegacyToken() (string, error) {
	b, err := os.ReadFile(tokenFile())
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", errors.New("the stored token file is empty")
	}
	return token, nil
}

// saveLegacyToken writes the old-style token file.
func saveLegacyToken(token string) (string, error) {
	path := tokenFile()
	if err := permissions.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil && !isWindows() {
		return "", err
	}
	restrictWindowsACL(path)
	return path, nil
}

// migrateLegacy gives an agent paired by an older version a configuration file,
// so that there is one place to edit. The old files are left where they are —
// a rollback to the older agent still finds its token — and are not read again
// once the new file says the same.
func migrateLegacy(c config) bool {
	if c.fileExists || (c.sources["token"] != srcLegacy && c.sources["server"] != srcLegacy) {
		return false
	}
	// Only what the old files held goes into the file: a value that arrived
	// from the environment or the command line this time is not a decision to
	// remember, so everything else starts from the defaults.
	legacy := defaultConfig()
	legacy.configPath = c.configPath
	if c.sources["token"] == srcLegacy {
		legacy.token = c.token
	}
	if c.sources["server"] == srcLegacy {
		legacy.server, legacy.certPin, legacy.insecure = c.server, c.certPin, c.insecure
	}
	if c.sources["name"] == srcLegacy {
		legacy.name = c.name
	}
	if err := persistConfig(legacy); err != nil {
		fmt.Fprintf(os.Stderr, "note: could not write %s (%v); the old token and settings files are still being used\n", c.configPath, err)
		return false
	}
	fmt.Printf("Settings from the older token and settings files are now in %s, which is the file to edit from here on.\n", c.configPath)
	return true
}

// ---- the config command ----

// runConfigCommand implements `gwatch-agent config [show|path|validate]`.
func runConfigCommand(sub string, c config, problems, warnings []string, showToken bool) error {
	switch sub {
	case "", "show":
		return showConfig(c, problems, warnings, showToken)
	case "path":
		fmt.Println(c.configPath)
		return nil
	case "validate":
		for _, w := range warnings {
			fmt.Println("warning:", w)
		}
		for _, p := range problems {
			fmt.Println("problem:", p)
		}
		for _, o := range overrides(c) {
			fmt.Println("note:", o)
		}
		if len(problems) > 0 || len(warnings) > 0 {
			return errors.New("the configuration needs attention")
		}
		fmt.Printf("%s is fine.\n", c.configPath)
		return nil
	}
	return fmt.Errorf("unknown config command %q: use show, path or validate", sub)
}

// overrides lists file settings that something higher up is overriding, which is
// why an edit would appear to do nothing.
func overrides(c config) []string {
	var out []string
	doc, err := loadConfigDoc(c.configPath)
	if err != nil {
		return nil
	}
	for _, k := range configKeys {
		if doc.has(k) && (c.sources[k] == srcEnv || c.sources[k] == srcFlag) {
			out = append(out, fmt.Sprintf("%s in %s is overridden by the %s", k, c.configPath, c.sources[k]))
		}
	}
	return out
}

func showConfig(c config, problems, warnings []string, showToken bool) error {
	state := "not created yet"
	if c.fileExists {
		state = "exists"
	}
	fmt.Printf("Config file: %s (%s)\n\n", c.configPath, state)
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 3, ' ', 0)
	row := func(key, value string) { fmt.Fprintf(tw, "%s\t%s\t(%s)\n", key, value, c.sources[key]) }
	row("server", orNone(c.server))
	row("token", describeToken(c.token, showToken))
	row("name", orDefault(c.name, "this computer's name"))
	row("interval", formatDuration(c.interval))
	row("checkIn", formatDuration(c.checkIn))
	row("buffer", formatDuration(c.buffer))
	row("autoUpdate", strconv.FormatBool(c.autoUpdate))
	row("insecure", strconv.FormatBool(c.insecure))
	row("certPin", orNone(c.certPin))
	row("listen", c.listen)
	row("repo", c.repo)
	row("hostRoot", orNone(c.hostRoot))
	row("collect", describeCollect(c.collect))
	row("collector", map[bool]string{true: "allowed: runs the checks GWatch assigns, if GWatch has also turned it on", false: "off"}[c.collector])
	tw.Flush()
	if !showToken && c.token != "" {
		fmt.Println("\nThe token is shortened here; run `gwatch-agent config show --show-token` to print it in full.")
	}
	for _, w := range warnings {
		fmt.Println("warning:", w)
	}
	for _, p := range problems {
		fmt.Println("problem:", p)
	}
	return nil
}

func orNone(s string) string { return orDefault(s, "none") }
func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return "(" + d + ")"
	}
	return s
}

// describeToken shows enough of a token to recognise it by — the same leading
// characters GWatch shows beside the machine — and the rest only on request.
func describeToken(t string, full bool) string {
	switch {
	case t == "":
		return "(none)"
	case full || len(t) <= 16:
		return t
	}
	return fmt.Sprintf("%s…%s (%d characters)", t[:12], t[len(t)-4:], len(t))
}

func describeCollect(c model.CollectSelection) string {
	if c.IsZero() {
		return "(automatic: everything, except virtual interfaces)"
	}
	b, _ := json.Marshal(c)
	return string(b)
}

// envBoolSet reads an on/off environment variable, reporting whether it said
// anything recognisable. Empty or misspelt means "not set", so it never turns
// off something like automatic updates by accident.
func envBoolSet(name string) (value, set bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}
