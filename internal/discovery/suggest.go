package discovery

import "sort"

// The ports a sweep probes by default. They are the ones that say something
// about what a device *is*, rather than the ones most likely to be open:
// knowing that 9100 answers turns "192.168.1.50" into "the printer", which is
// the whole point of showing the list to a person.
//
//	22    SSH — a computer someone administers
//	80    HTTP — a web interface (a router's, a NAS's, a camera's)
//	443   HTTPS — the same, secured
//	445   SMB — a Windows machine or a NAS sharing files
//	3389  RDP — a Windows desktop or server
//	8080  HTTP alternate — appliances and admin pages
//	8443  HTTPS alternate — the same
//	9100  JetDirect — a network printer
//	32400 Plex
//	1883  MQTT — a broker, so usually the heart of a home automation setup
//
// SNMP (161) is deliberately absent: it is UDP, and a UDP probe that gets no
// answer is indistinguishable from one that was dropped, so "no reply" would
// mean nothing. The SNMP check type is where that conversation belongs.
var DefaultPorts = []int{22, 80, 443, 445, 3389, 8080, 8443, 9100, 32400, 1883}

// MaxPorts caps a caller-supplied port list. Ten ports on 4096 hosts is
// already 40,000 connections; a list longer than this is a port scan wearing
// discovery's clothes.
const MaxPorts = 24

// webPorts are the ports that mean "this thing has a web interface".
var webPorts = []int{80, 443, 8080, 8443}

// Suggestion is a starting template for a responder, with the reason it was
// picked. Both are shown in the results list, and both can be overridden
// before anything is created — the table guesses, it does not decide.
type Suggestion struct {
	Template string `json:"template"`
	Note     string `json:"note,omitempty"`
}

// suggestRule is one row of the table below. The first rule whose match
// returns true wins, so the rows are ordered from most specific to least.
type suggestRule struct {
	match func(open portSet) bool
	give  Suggestion
}

// suggestRules maps what answered to what to create. Every row is a guess
// about a kind of device rather than about a port, which is why the notes read
// the way they do: the person picking from this list is looking for "the
// printer", not for "9100".
var suggestRules = []suggestRule{
	{
		// SSH plus a web interface is a server or a NAS: something with a
		// login and a page, which is exactly what the home-server template
		// watches (ping, 22, and the web interface).
		func(open portSet) bool { return open.has(22) && open.hasAny(webPorts...) },
		Suggestion{"home-server", "SSH and a web interface — looks like a server or NAS."},
	},
	{
		// Plex announces itself on 32400 and nothing else does.
		func(open portSet) bool { return open.has(32400) },
		Suggestion{"tcp-service", "Port 32400 is open — this looks like Plex."},
	},
	{
		// JetDirect. A printer with a web page would otherwise be read as a
		// router, which is why this sits above the web-only rule.
		func(open portSet) bool { return open.has(9100) },
		Suggestion{"tcp-service", "Port 9100 is open — this looks like a network printer."},
	},
	{
		// Remote desktop. There is no Windows template to send this to, so it
		// gets a plain reachability check and a note saying what it is; the
		// hardware agent is the honest way to watch a Windows machine.
		func(open portSet) bool { return open.has(3389) },
		Suggestion{"ping", "Remote desktop is open — this looks like a Windows machine. Install the agent on it for hardware health."},
	},
	{
		// MQTT: a broker is usually the piece everything else depends on.
		func(open portSet) bool { return open.has(1883) },
		Suggestion{"tcp-service", "Port 1883 is open — this looks like an MQTT broker."},
	},
	{
		// Only web ports answered: a router, switch, access point or camera —
		// a box whose entire interface is its admin page.
		func(open portSet) bool { return open.onlyAmong(webPorts...) && open.hasAny(webPorts...) },
		Suggestion{"router", "Only a web interface answered — looks like a router, switch or access point."},
	},
	{
		// SSH on its own, with no page to go with it.
		func(open portSet) bool { return open.has(22) },
		Suggestion{"home-server", "SSH is open — looks like a computer you can log in to."},
	},
	{
		// File sharing and nothing else.
		func(open portSet) bool { return open.has(445) },
		Suggestion{"ping", "File sharing is open — a Windows machine or a NAS."},
	},
}

// Suggest picks a starting template from the ports that answered. A device
// that answered a ping and nothing else still gets a template: it is there, it
// is worth knowing when it stops being there, and that is the ping template.
func Suggest(openPorts []int) Suggestion {
	open := portSet{}
	for _, p := range openPorts {
		open[p] = true
	}
	for _, rule := range suggestRules {
		if rule.match(open) {
			return rule.give
		}
	}
	return Suggestion{Template: "ping"}
}

// portSet is the set of ports that answered, with the handful of questions the
// table above asks of it.
type portSet map[int]bool

func (s portSet) has(p int) bool { return s[p] }

func (s portSet) hasAny(ports ...int) bool {
	for _, p := range ports {
		if s[p] {
			return true
		}
	}
	return false
}

// onlyAmong reports whether every open port is one of the given ones.
func (s portSet) onlyAmong(ports ...int) bool {
	allowed := portSet{}
	for _, p := range ports {
		allowed[p] = true
	}
	for p, open := range s {
		if open && !allowed[p] {
			return false
		}
	}
	return true
}

// normalizePorts validates a caller's port list, dropping duplicates and
// keeping the order tidy.
//
// No list at all (nil) means the default set. A list that is present but empty
// means exactly that: probe nothing, and describe each responder by its name
// and its round trip alone. The distinction is worth keeping — it is the
// difference between "you decide" and "don't", and a sweep that touches no
// ports is both faster and less intrusive.
func normalizePorts(ports []int) ([]int, error) {
	if ports == nil {
		return append([]int(nil), DefaultPorts...), nil
	}
	if len(ports) == 0 {
		return []int{}, nil
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(ports))
	for _, p := range ports {
		if p < 1 || p > 65535 {
			return nil, portRangeError(p)
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) > MaxPorts {
		return nil, tooManyPortsError(len(out))
	}
	sort.Ints(out)
	return out, nil
}
