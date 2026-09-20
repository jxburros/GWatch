package discovery

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/checks"
)

// The timings a sweep runs at. They are short on purpose: a device on the
// local network that is going to answer answers in single-digit milliseconds,
// and the cost of these numbers is paid once per address in the range.
const (
	pingTimeout   = 750 * time.Millisecond // per ping attempt
	lookupTimeout = 1 * time.Second        // reverse DNS for one responder
	probeTimeout  = 400 * time.Millisecond // one TCP connect
	defaultWorker = 64                     // addresses pinged at once
	progressEvery = 250 * time.Millisecond // how often progress is reported
)

// These three are the package's only contact with the network, and they are
// variables so the tests can sweep an imaginary house full of devices without
// sending a packet. Production never reassigns them.
var (
	// pingHost is the exchange a ping check performs, privilege fallbacks and
	// all — see checks.PingHost. A device that answers a sweep therefore
	// answers the check the sweep suggests, which is the only promise the
	// results list is really making.
	pingHost = checks.PingHost

	// lookupAddr asks for the name behind an address.
	lookupAddr = func(ctx context.Context, addr string) ([]string, error) {
		return net.DefaultResolver.LookupAddr(ctx, addr)
	}

	// dialPort opens and immediately closes a TCP connection.
	dialPort = func(ctx context.Context, addr string) error {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		return conn.Close()
	}
)

// Responder is one address that answered, described well enough to become a
// node. Everything but the address is best effort: a device with no reverse
// DNS entry and no open port is still a device.
type Responder struct {
	IP        string  `json:"ip"`
	Hostname  string  `json:"hostname,omitempty"`
	RTTMS     float64 `json:"rttMs"`
	OpenPorts []int   `json:"openPorts"`
	Template  string  `json:"template"`
	Note      string  `json:"note,omitempty"`
}

// Name is what the responder should be called if nobody types anything: its
// reverse-DNS name if it has one, its address otherwise.
func (r Responder) Name() string {
	if r.Hostname != "" {
		return r.Hostname
	}
	return r.IP
}

// Progress is how far a sweep has got. It is what the modal's progress bar is
// drawn from and what rides on the update stream.
type Progress struct {
	Scanned    int
	Total      int
	Responders int
}

// sweep pings every address, then describes the ones that answered.
//
// The two phases are deliberately separate. Reverse DNS and the port probes
// are only worth doing for a responder, and doing them inline would hold a
// ping worker for a second and a half per hit — on a network where most of the
// hits are consecutive, that is the whole sweep waiting on the resolver.
func sweep(ctx context.Context, addrs []netip.Addr, ports []int, workers int, report func(Progress)) []Responder {
	if workers <= 0 {
		workers = defaultWorker
	}
	if workers > len(addrs) {
		workers = len(addrs)
	}

	var (
		mu      sync.Mutex
		hits    []hit
		scanned int
		last    time.Time
	)
	// Progress is reported on a timer rather than per address: a /24 at 64
	// workers finishes in a couple of seconds, and 254 broadcasts to every
	// connected browser to say so would be noise.
	tick := func() {
		mu.Lock()
		defer mu.Unlock()
		scanned++
		if report == nil {
			return
		}
		now := time.Now()
		if scanned < len(addrs) && now.Sub(last) < progressEvery {
			return
		}
		last = now
		report(Progress{Scanned: scanned, Total: len(addrs), Responders: len(hits)})
	}

	queue := make(chan netip.Addr)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range queue {
				if ctx.Err() != nil {
					return
				}
				if rtt, ok := pingOnce(ctx, a); ok {
					mu.Lock()
					hits = append(hits, hit{addr: a, rtt: rtt})
					mu.Unlock()
				}
				tick()
			}
		}()
	}
	// The send is a select rather than a plain one, because a cancelled run
	// takes its workers down: without this, the last address would be offered
	// to a queue nobody is reading from any more and the sweep would hang.
feed:
	for _, a := range addrs {
		select {
		case queue <- a:
		case <-ctx.Done():
			break feed
		}
	}
	close(queue)
	wg.Wait()

	sort.Slice(hits, func(i, j int) bool { return hits[i].addr.Less(hits[j].addr) })
	return describe(ctx, hits, ports)
}

type hit struct {
	addr netip.Addr
	rtt  time.Duration
}

// pingOnce sends a single echo request, and sends a second one if the first
// went unanswered. One lost packet on a busy wireless network is ordinary; a
// device dropped from the results because of it is not.
func pingOnce(ctx context.Context, a netip.Addr) (time.Duration, bool) {
	ip := a.String()
	for attempt := 0; attempt < 2; attempt++ {
		if ctx.Err() != nil {
			return 0, false
		}
		_, received, rtts, err := pingHost(ctx, ip, 1, pingTimeout)
		if err == nil && received > 0 {
			if len(rtts) > 0 {
				return rtts[0], true
			}
			return 0, true
		}
	}
	return 0, false
}

// describe fills in the name and the open ports for each responder. The hosts
// are handled one after another rather than in parallel: there are only ever a
// handful of them, and a resolver asked for two hundred names at once is a
// resolver that starts refusing.
//
// A cancelled run still describes what it found, minus the lookups: those
// devices answered, and a list of bare addresses is more use than no list.
func describe(ctx context.Context, hits []hit, ports []int) []Responder {
	out := make([]Responder, 0, len(hits))
	for _, h := range hits {
		ip := h.addr.String()
		r := Responder{
			IP:        ip,
			RTTMS:     float64(h.rtt) / float64(time.Millisecond),
			OpenPorts: []int{},
		}
		if ctx.Err() == nil {
			r.Hostname = reverseName(ctx, ip)
			r.OpenPorts = probePorts(ctx, ip, ports)
		}
		s := Suggest(r.OpenPorts)
		r.Template, r.Note = s.Template, s.Note
		out = append(out, r)
	}
	return out
}

// reverseName returns the name behind an address, or "" — which is the
// ordinary answer on a network whose router does not publish its leases.
func reverseName(ctx context.Context, ip string) string {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	names, err := lookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	// Resolvers return names with the root dot on them; nobody wants to read
	// "nas.local." in a list.
	name := names[0]
	for len(name) > 0 && name[len(name)-1] == '.' {
		name = name[:len(name)-1]
	}
	return name
}

// probePorts tries each port in turn and reports the ones that accepted a
// connection. The probes for one host run together — a device that answers a
// ping will refuse a closed port immediately, so the whole list costs about
// one timeout in the worst case rather than one per port.
func probePorts(ctx context.Context, ip string, ports []int) []int {
	if len(ports) == 0 {
		return []int{}
	}
	var (
		mu   sync.Mutex
		open []int
		wg   sync.WaitGroup
	)
	for _, p := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			if err := dialPort(pctx, net.JoinHostPort(ip, fmt.Sprint(p))); err == nil {
				mu.Lock()
				open = append(open, p)
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()
	sort.Ints(open)
	if open == nil {
		open = []int{}
	}
	return open
}

func portRangeError(p int) error {
	return fmt.Errorf("%d is not a port number; ports run from 1 to 65535", p)
}

func tooManyPortsError(n int) error {
	return fmt.Errorf("%d ports is more than discovery will probe; keep the list to %d or fewer", n, MaxPorts)
}
