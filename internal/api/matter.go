package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/matter"
	"github.com/jxburros/GWatch/internal/model"
)

// matterListTimeout bounds "Load devices". get_nodes answers from the
// sidecar's memory, so anything slower than this is a sidecar in trouble.
const matterListTimeout = 20 * time.Second

// matterCommissionTimeout bounds commissioning. The sidecar discovers the
// device, establishes a PASE session, sends it the fabric's certificates and
// then interviews every endpoint, which for a bridge with dozens of devices
// takes well over a minute.
const matterCommissionTimeout = 3 * time.Minute

// matterServerFromBody works out which sidecar a request means: the address
// given, or the node host on the default port and path when only a host is.
// It returns the URL, or writes a 400 and returns "".
func matterServerFromBody(w http.ResponseWriter, server, host string) string {
	u := matter.ServerURL(server, host)
	if u == "" {
		writeError(w, http.StatusBadRequest, "a Matter server address (or a host) is required")
		return ""
	}
	if err := matter.ValidateServerURL(u); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return ""
	}
	return u
}

// matterDialError turns a failed connection into the sentence the editor
// shows under the "Load devices" button.
func matterDialError(err error, serverURL string) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("the Matter server at %s did not answer in time — is python-matter-server running there?", serverURL)
	}
	return fmt.Sprintf("could not reach the Matter server at %s: %v", serverURL, err)
}

// handleMatterNodes asks a Matter server which nodes it has commissioned and
// describes each one — vendor, product, label, and for every endpoint the
// device types and the standard readings it offers with their current values
// — so the editor can offer a list to pick from rather than asking somebody
// to know a node id, an endpoint number and a cluster id.
//
// It is administrator-only and refused to API keys of every scope, for the
// same reason as POST /api/snmp/walk: it makes GWatch connect to an address
// of the caller's choosing and reports what answered. That is a probe, and a
// probe belongs to the person in front of the machine.
func (s *Server) handleMatterNodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Server string `json:"server"`
		Host   string `json:"host"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	serverURL := matterServerFromBody(w, body.Server, body.Host)
	if serverURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), matterListTimeout)
	defer cancel()
	client, err := matter.Dial(ctx, serverURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, matterDialError(err, serverURL))
		return
	}
	defer client.Close()
	nodes, err := client.GetNodes(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := make([]matter.NodeSummary, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, matter.Summarize(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"server":     serverURL,
		"serverInfo": client.Info,
		"nodes":      out,
		"standards":  matter.Standards,
	})
}

// pairingCode accepts what a Matter device's label prints: a QR code payload
// ("MT:" followed by base-38 text) or an 11- or 21-digit manual pairing code,
// which labels often print with dashes or spaces between groups.
var pairingCode = regexp.MustCompile(`^(MT:[0-9A-Z.\-]{10,}|[0-9]{11}|[0-9]{21})$`)

// normalizePairingCode strips the separators people type or copy from a
// label, and refuses anything that is not a pairing code at all before it
// reaches the sidecar.
func normalizePairingCode(code string) (string, error) {
	c := strings.TrimSpace(code)
	if !strings.HasPrefix(strings.ToUpper(c), "MT:") {
		c = strings.NewReplacer("-", "", " ", "").Replace(c)
	} else {
		c = "MT:" + c[3:]
	}
	if !pairingCode.MatchString(c) {
		return "", errors.New("that is not a Matter pairing code: use the 11-digit manual code (dashes are fine) or the QR code text (it starts with MT:) as it is")
	}
	return c, nil
}

// handleMatterCommission adds a device to the sidecar's fabric with a pairing
// code. For a device already in another ecosystem (Apple Home, Google Home,
// Alexa, SmartThings, Home Assistant), the code comes from that app's "share
// with another controller" or "turn on pairing mode" screen — Matter's
// multi-admin — and the device stays in that ecosystem as well.
//
// GWatch only relays the code: the fabric's certificates and the device's
// operational credentials are created and kept by the sidecar, in its own
// storage, and nothing of them passes through or is stored here. The request
// is recorded in the event log, because it changes what a controller on the
// network can do.
func (s *Server) handleMatterCommission(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Server      string `json:"server"`
		Host        string `json:"host"`
		Code        string `json:"code"`
		NetworkOnly *bool  `json:"networkOnly"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	serverURL := matterServerFromBody(w, body.Server, body.Host)
	if serverURL == "" {
		return
	}
	code, err := normalizePairingCode(body.Code)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Network-only is the default: it is what adding an already-installed
	// device through multi-admin needs, and Bluetooth commissioning only
	// works where the sidecar has an adapter and the D-Bus socket.
	networkOnly := body.NetworkOnly == nil || *body.NetworkOnly

	ctx, cancel := context.WithTimeout(r.Context(), matterCommissionTimeout)
	defer cancel()
	client, err := matter.Dial(ctx, serverURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, matterDialError(err, serverURL))
		return
	}
	defer client.Close()
	if !networkOnly && !client.Info.BluetoothEnabled {
		writeError(w, http.StatusBadRequest, "this Matter server has no Bluetooth, so it can only add devices that are already on the network — tick “already on the network”, or start the server with --bluetooth-adapter")
		return
	}
	node, err := client.Commission(ctx, code, networkOnly)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "commissioning did not finish within " + matterCommissionTimeout.String() + "; the device may still appear on the Matter server — load its devices again in a minute"
		}
		s.recordEvent(r.Context(), model.Event{Type: model.EventConfigChanged, Title: "Matter commissioning failed", Detail: fmt.Sprintf("%s: %s", serverURL, msg)})
		writeError(w, http.StatusBadGateway, msg)
		return
	}
	summary := matter.Summarize(node)
	s.recordEvent(r.Context(), model.Event{Type: model.EventConfigChanged, Title: "Matter device commissioned",
		Detail: fmt.Sprintf("node %d (%s) added to the fabric of the Matter server at %s", node.NodeID, strings.TrimSpace(summary.Vendor+" "+summary.Product), serverURL)})
	writeJSON(w, http.StatusOK, map[string]any{"server": serverURL, "node": summary})
}
