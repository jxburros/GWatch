package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// The states a run passes through. A run is only ever in one of them, and
// three of the four are final.
const (
	StateRunning   = "running"
	StateDone      = "done"
	StateCancelled = "cancelled"
	StateFailed    = "failed"
)

// ErrBusy is returned when a sweep is already in flight. One run at a time is
// not a limitation anyone notices: a sweep saturates the same ping path the
// monitoring checks use, and two of them at once would make both of them and
// every check slower for no gain.
var ErrBusy = errors.New("a discovery run is already in progress")

// ErrNotFound is returned for an id that is not the run the registry holds.
var ErrNotFound = errors.New("no discovery run with that id")

// Job is a discovery run as the API reports it: what was asked for, how far it
// has got, and what it found. It is a snapshot — the registry hands out copies
// so a reader never sees a half-written result slice.
type Job struct {
	ID         string      `json:"id"`
	Ranges     []string    `json:"ranges"`
	Ports      []int       `json:"ports"`
	State      string      `json:"state"`
	Total      int         `json:"total"`
	Scanned    int         `json:"scanned"`
	Responders int         `json:"responders"`
	Results    []Responder `json:"results"`
	Error      string      `json:"error,omitempty"`
	StartedAt  time.Time   `json:"startedAt"`
	FinishedAt *time.Time  `json:"finishedAt,omitempty"`
}

// Done reports whether the run has stopped, however it stopped.
func (j Job) Done() bool { return j.State != StateRunning }

// Summary is the one-line description of a run, for the audit timeline:
// "254 addresses in 192.168.1.0/24: 17 responded".
func (j Job) Summary() string {
	word := "addresses"
	if j.Scanned == 1 {
		word = "address"
	}
	where := ""
	if len(j.Ranges) > 0 {
		where = " in " + strings.Join(j.Ranges, ", ")
	}
	return fmt.Sprintf("%d %s%s: %d responded", j.Scanned, word, where, j.Responders)
}

// Request is what a caller asks for.
type Request struct {
	Ranges []string `json:"ranges"`
	Ports  []int    `json:"ports"`
	// Workers overrides the sweep's concurrency; zero means the default.
	Workers int `json:"-"`
}

// Registry holds the one discovery run an install may have in flight, and
// keeps the last finished one around so a modal that was closed and reopened
// can pick the results back up. It is in memory on purpose: a sweep is a
// question about the network as it is right now, and an answer that outlives a
// restart is an answer about a network that has moved on.
type Registry struct {
	// Sweep does the scanning. It is this package's own sweep unless something
	// replaces it, which is how a test can drive a registry — or the API
	// routes above it — over an imaginary network. The monitoring engine takes
	// its check runner the same way; see engine.Options.Run.
	Sweep SweepFunc

	mu      sync.Mutex
	job     Job
	cancel  context.CancelFunc
	running bool
}

// SweepFunc scans a list of addresses and describes the ones that answered,
// calling report as it goes.
type SweepFunc func(ctx context.Context, addrs []netip.Addr, ports []int, workers int, report func(Progress)) []Responder

// Start begins a sweep and returns the job as it stands at that moment. The
// sweep itself runs on its own goroutine, under its own context: an HTTP
// request that has already been answered must not take the run down with it.
//
// notify is called with a fresh snapshot each time the job moves — a few times
// a second while scanning, and once more when it finishes. It is called from
// the sweep's goroutine, so it should hand the snapshot on rather than do
// anything slow with it.
func (r *Registry) Start(req Request, notify func(Job)) (Job, error) {
	addrs, err := ParseRanges(req.Ranges)
	if err != nil {
		return Job{}, err
	}
	ports, err := normalizePorts(req.Ports)
	if err != nil {
		return Job{}, err
	}

	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return Job{}, ErrBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := Job{
		ID:        newID(),
		Ranges:    cleanRanges(req.Ranges),
		Ports:     ports,
		State:     StateRunning,
		Total:     len(addrs),
		Results:   []Responder{},
		StartedAt: time.Now(),
	}
	r.job, r.cancel, r.running = job, cancel, true
	scan := r.Sweep
	r.mu.Unlock()
	if scan == nil {
		scan = sweep
	}

	go func() {
		defer cancel()
		results := scan(ctx, addrs, ports, req.Workers, func(p Progress) {
			if snap, ok := r.progress(job.ID, p); ok && notify != nil {
				notify(snap)
			}
		})
		snap := r.finish(job.ID, results, ctx.Err())
		if notify != nil {
			notify(snap)
		}
	}()

	return job, nil
}

// progress writes a running job's counters and returns the snapshot to
// broadcast. It reports false once the run has finished or been replaced, so a
// straggling worker cannot resurrect a cancelled job's progress bar.
func (r *Registry) progress(id string, p Progress) (Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.job.ID != id || r.job.State != StateRunning {
		return Job{}, false
	}
	r.job.Scanned, r.job.Total, r.job.Responders = p.Scanned, p.Total, p.Responders
	return r.job.clone(), true
}

// finish records how a run ended. A cancelled run keeps whatever it found
// before it was stopped: those devices are still there, and a half-finished
// list is more use than no list.
func (r *Registry) finish(id string, results []Responder, ctxErr error) Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.job.ID != id {
		return r.job.clone()
	}
	r.running = false
	r.cancel = nil
	if results == nil {
		results = []Responder{}
	}
	r.job.Results = results
	r.job.Responders = len(results)
	if ctxErr == nil {
		// A run that was not interrupted got through everything it was given,
		// whatever the last progress tick happened to record.
		r.job.Scanned = r.job.Total
	}
	switch {
	case errors.Is(ctxErr, context.Canceled):
		r.job.State = StateCancelled
	case ctxErr != nil:
		r.job.State = StateFailed
		r.job.Error = ctxErr.Error()
	default:
		r.job.State = StateDone
	}
	now := time.Now()
	r.job.FinishedAt = &now
	return r.job.clone()
}

// Get returns a run by id.
func (r *Registry) Get(id string) (Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.job.ID == "" || r.job.ID != id {
		return Job{}, ErrNotFound
	}
	return r.job.clone(), nil
}

// Latest returns the most recent run, running or finished. It is what a
// reopened modal reattaches to.
func (r *Registry) Latest() (Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.job.ID == "" {
		return Job{}, ErrNotFound
	}
	return r.job.clone(), nil
}

// Cancel stops a running sweep. Cancelling one that has already stopped is not
// an error: the caller asked for it to be stopped, and it is.
func (r *Registry) Cancel(id string) (Job, error) {
	r.mu.Lock()
	if r.job.ID == "" || r.job.ID != id {
		r.mu.Unlock()
		return Job{}, ErrNotFound
	}
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// The sweep's own goroutine writes the final state, so wait briefly for it
	// rather than reporting "running" to a caller who just stopped the run.
	for i := 0; i < 100; i++ {
		r.mu.Lock()
		job := r.job.clone()
		done := !r.running
		r.mu.Unlock()
		if done {
			return job, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return r.Get(id)
}

// Running reports whether a sweep is in flight.
func (r *Registry) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// clone copies the slices a caller could otherwise hold a live reference to.
func (j Job) clone() Job {
	out := j
	out.Ranges = append([]string(nil), j.Ranges...)
	out.Ports = append([]int(nil), j.Ports...)
	out.Results = make([]Responder, len(j.Results))
	for i, r := range j.Results {
		r.OpenPorts = append([]int(nil), r.OpenPorts...)
		out.Results[i] = r
	}
	if j.FinishedAt != nil {
		t := *j.FinishedAt
		out.FinishedAt = &t
	}
	return out
}

// cleanRanges tidies the ranges for display and for the audit line: the text
// the person typed, minus the blank lines and the stray whitespace.
func cleanRanges(lines []string) []string {
	var out []string
	for _, line := range lines {
		out = append(out, splitSpecs(line)...)
	}
	return out
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A clock-derived id is still unique enough for one run at a time.
		return hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000000")))
	}
	return hex.EncodeToString(b[:])
}
