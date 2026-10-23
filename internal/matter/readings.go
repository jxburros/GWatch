package matter

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Cluster ids GWatch understands, from the Matter Application Cluster
// Specification. They are written in hex there and in decimal in the
// server's attribute paths ("1/1026/0" is endpoint 1, TemperatureMeasurement,
// MeasuredValue).
const (
	ClusterDescriptor         = 0x001D
	ClusterBasicInformation   = 0x0028
	ClusterBridgedDeviceBasic = 0x0039
	ClusterPowerSource        = 0x002F
	ClusterBooleanState       = 0x0045
	ClusterOnOff              = 0x0006
	ClusterIlluminance        = 0x0400
	ClusterTemperature        = 0x0402
	ClusterRelativeHumidity   = 0x0405
	ClusterOccupancySensing   = 0x0406
	attrPowerSourceBatPercent = 0x000C
	attrBasicVendorName       = 1
	attrBasicProductName      = 3
	attrBasicNodeLabel        = 5
	attrDescriptorDeviceTypes = 0
	rootEndpoint              = 0
)

// Kind is one of the standard readings GWatch knows how to interpret, or
// KindAttribute for any other numeric attribute read as it stands.
type Kind string

const (
	KindOnOff       Kind = "onoff"
	KindTemperature Kind = "temperature"
	KindHumidity    Kind = "humidity"
	KindContact     Kind = "contact"
	KindOccupancy   Kind = "occupancy"
	KindIlluminance Kind = "illuminance"
	KindBattery     Kind = "battery"
	KindAttribute   Kind = "attribute"
)

// Standard describes one standard reading: where it lives, what it is called
// by default, what unit it is in, and how the raw attribute becomes a number
// and a word.
type Standard struct {
	Kind      Kind   `json:"kind"`
	Label     string `json:"label"` // what the editor offers it as
	Name      string `json:"name"`  // the default metric name
	Unit      string `json:"unit"`
	Cluster   uint32 `json:"cluster"`
	Attribute uint32 `json:"attribute"`
	Help      string `json:"help"`
}

// Standards lists the standard readings in the order the editor offers them.
// Each conversion follows the cluster's specification; the comments on
// Interpret say how.
var Standards = []Standard{
	{KindTemperature, "Temperature", "Temperature", "°C", ClusterTemperature, 0, "TemperatureMeasurement › MeasuredValue, in hundredths of a degree Celsius."},
	{KindHumidity, "Relative humidity", "Humidity", "%", ClusterRelativeHumidity, 0, "RelativeHumidityMeasurement › MeasuredValue, in hundredths of a percent."},
	{KindOnOff, "On / off", "On", "", ClusterOnOff, 0, "OnOff › OnOff: 1 when on, 0 when off. A plug, a light or a switch."},
	{KindContact, "Contact (open / shut)", "Open", "", ClusterBooleanState, 0, "BooleanState › StateValue: 1 when open, 0 when shut. A door or window sensor."},
	{KindOccupancy, "Occupancy", "Occupied", "", ClusterOccupancySensing, 0, "OccupancySensing › Occupancy, bit 0: 1 when someone is there."},
	{KindIlluminance, "Illuminance", "Illuminance", "lx", ClusterIlluminance, 0, "IlluminanceMeasurement › MeasuredValue, converted from its logarithmic scale to lux."},
	{KindBattery, "Battery", "Battery", "%", ClusterPowerSource, attrPowerSourceBatPercent, "PowerSource › BatPercentRemaining, in half-percent steps."},
}

// StandardFor returns the description of a standard kind.
func StandardFor(k Kind) (Standard, bool) {
	for _, s := range Standards {
		if s.Kind == k {
			return s, true
		}
	}
	return Standard{}, false
}

// Path builds the server's attribute path for one attribute.
func Path(endpoint, cluster, attribute uint32) string {
	return fmt.Sprintf("%d/%d/%d", endpoint, cluster, attribute)
}

// Interpreted is what one attribute value means.
type Interpreted struct {
	Value   *float64 // nil when the value is not a number (or is unknown)
	Text    string   // how a person reads it: "on", "21.5", "open"
	Missing bool     // the device has no value for it: absent or null
}

// Interpret turns one raw attribute value into a number and a word. raw is
// the JSON the server sent; a missing or null value is Missing, because every
// one of these attributes is nullable and null means "the sensor does not
// know right now".
//
//   - onoff: a boolean; 1 on, 0 off.
//   - temperature, humidity: a signed or unsigned integer in hundredths.
//   - contact: BooleanState's StateValue is true when the contact is made,
//     which for a door or window sensor means shut. The metric is the other
//     way round — 1 open, 0 shut — so that "alert when open" is a critical
//     threshold above 0, like every other "too much" reading.
//   - occupancy: a bitmap whose bit 0 is "occupied".
//   - illuminance: MeasuredValue = 10000 × log10(lux) + 1, so lux =
//     10^((v−1)/10000). 0 means "too dark to measure" and reads as 0 lux.
//   - battery: BatPercentRemaining counts half percents, 200 being full.
//   - attribute: any number, or a boolean as 1/0, multiplied by scale; other
//     values are kept as text and never thresholded.
func Interpret(kind Kind, raw json.RawMessage, scale float64) Interpreted {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return Interpreted{Missing: true}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return Interpreted{Text: s}
	}
	num, isNum := v.(float64)
	b, isBool := v.(bool)
	f := func(x float64) *float64 { return &x }
	switch kind {
	case KindOnOff:
		on := (isBool && b) || (isNum && num != 0)
		if !isBool && !isNum {
			return Interpreted{Text: s}
		}
		if on {
			return Interpreted{Value: f(1), Text: "on"}
		}
		return Interpreted{Value: f(0), Text: "off"}
	case KindContact:
		if !isBool && !isNum {
			return Interpreted{Text: s}
		}
		contact := (isBool && b) || (isNum && num != 0)
		if contact {
			return Interpreted{Value: f(0), Text: "shut"}
		}
		return Interpreted{Value: f(1), Text: "open"}
	case KindOccupancy:
		if !isNum {
			return Interpreted{Text: s}
		}
		if int64(num)&1 == 1 {
			return Interpreted{Value: f(1), Text: "occupied"}
		}
		return Interpreted{Value: f(0), Text: "clear"}
	case KindTemperature, KindHumidity:
		if !isNum {
			return Interpreted{Text: s}
		}
		x := num / 100
		return Interpreted{Value: f(x), Text: formatNumber(x)}
	case KindIlluminance:
		if !isNum {
			return Interpreted{Text: s}
		}
		lux := 0.0
		if num > 0 {
			lux = math.Pow(10, (num-1)/10000)
		}
		return Interpreted{Value: f(lux), Text: formatNumber(lux)}
	case KindBattery:
		if !isNum {
			return Interpreted{Text: s}
		}
		x := num / 2
		return Interpreted{Value: f(x), Text: formatNumber(x)}
	default:
		if scale == 0 {
			scale = 1
		}
		switch {
		case isNum:
			x := num * scale
			return Interpreted{Value: f(x), Text: formatNumber(x)}
		case isBool:
			x := 0.0
			if b {
				x = 1
			}
			return Interpreted{Value: f(x * scale), Text: strconv.FormatBool(b)}
		case v == nil:
			return Interpreted{Missing: true}
		}
		if str, ok := v.(string); ok {
			return Interpreted{Text: str}
		}
		return Interpreted{Text: s}
	}
}

// formatNumber prints a value to two decimals at most, without trailing
// zeros, which is as much precision as any of these sensors has.
func formatNumber(x float64) string {
	return strconv.FormatFloat(math.Round(x*100)/100, 'f', -1, 64)
}

// ServerURL is the address a check or the editor talks to: the one given, or
// the default port and path on host when none was. A bare "host:port" is
// given the ws:// scheme and the /ws path.
func ServerURL(configured, host string) string {
	u := strings.TrimSpace(configured)
	if u == "" {
		h := strings.TrimSpace(host)
		if h == "" {
			return ""
		}
		// A node host may itself be a URL someone pasted; only its host part
		// is meaningful here.
		if pu, err := url.Parse(h); err == nil && pu.Host != "" {
			h = pu.Hostname()
		}
		return "ws://" + net.JoinHostPort(strings.Trim(h, "[]"), strconv.Itoa(DefaultPort)) + DefaultPath
	}
	if !strings.Contains(u, "://") {
		u = "ws://" + u
		if pu, err := url.Parse(u); err == nil && (pu.Path == "" || pu.Path == "/") {
			pu.Path = DefaultPath
			if pu.Port() == "" {
				pu.Host = net.JoinHostPort(pu.Hostname(), strconv.Itoa(DefaultPort))
			}
			u = pu.String()
		}
	}
	return u
}

// ValidateServerURL refuses an address the client could not dial.
func ValidateServerURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid Matter server URL %q", raw)
	}
	if s := strings.ToLower(u.Scheme); s != "ws" && s != "wss" {
		return fmt.Errorf("the Matter server URL must start with ws:// or wss:// (for example ws://192.168.1.20:%d%s)", DefaultPort, DefaultPath)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("the Matter server URL %q has no host", raw)
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("the Matter server URL %q has an invalid port", raw)
		}
	}
	return nil
}

// ---- summarising a node for the picker -------------------------------------

// NodeSummary is a node the way the editor's picker shows it: who made it,
// what it is called, and for each endpoint which standard readings it has and
// what they read at the server's last update.
type NodeSummary struct {
	NodeID    uint64            `json:"nodeId"`
	Available bool              `json:"available"`
	IsBridge  bool              `json:"isBridge"`
	Vendor    string            `json:"vendor,omitempty"`
	Product   string            `json:"product,omitempty"`
	Label     string            `json:"label,omitempty"`
	Endpoints []EndpointSummary `json:"endpoints"`
}

// EndpointSummary is one endpoint of a node. A bridge has one per device it
// bridges, each with its own label.
type EndpointSummary struct {
	ID          uint32           `json:"id"`
	Label       string           `json:"label,omitempty"`
	DeviceTypes []string         `json:"deviceTypes,omitempty"`
	Clusters    []uint32         `json:"clusters"`
	Readings    []ReadingSummary `json:"readings,omitempty"`
}

// ReadingSummary is one standard reading an endpoint offers, with its cached
// value so the picker can show "21.4 °C" beside it.
type ReadingSummary struct {
	Kind      Kind   `json:"kind"`
	Name      string `json:"name"`
	Unit      string `json:"unit,omitempty"`
	Cluster   uint32 `json:"cluster"`
	Attribute uint32 `json:"attribute"`
	Current   string `json:"current,omitempty"`
}

// deviceTypeNames names the device types a home network is most likely to
// hold. The list is not exhaustive; an unknown type is shown by number.
var deviceTypeNames = map[uint32]string{
	0x000E: "Aggregator (bridge)",
	0x0011: "Power source",
	0x0013: "Bridged node",
	0x0015: "Contact sensor",
	0x0016: "Root node",
	0x0043: "Water leak detector",
	0x0076: "Smoke/CO alarm",
	0x0100: "On/off light",
	0x0101: "Dimmable light",
	0x010A: "On/off plug-in unit",
	0x010B: "Dimmable plug-in unit",
	0x010C: "Colour temperature light",
	0x010D: "Extended colour light",
	0x0103: "On/off light switch",
	0x0106: "Light sensor",
	0x0107: "Occupancy sensor",
	0x0202: "Window covering",
	0x0301: "Thermostat",
	0x0302: "Temperature sensor",
	0x0307: "Humidity sensor",
	0x000A: "Door lock",
	0x002B: "Fan",
	0x002C: "Air quality sensor",
	0x0510: "Electrical sensor",
}

// Summarize describes a node from the server's cached attributes. Nothing is
// read from the device: this is what the server already knows, which is
// enough to pick from and costs the device nothing.
func Summarize(n NodeData) NodeSummary {
	out := NodeSummary{NodeID: n.NodeID, Available: n.Available, IsBridge: n.IsBridge}
	type epData struct {
		clusters map[uint32]bool
		attrs    map[[2]uint32]json.RawMessage
	}
	eps := map[uint32]*epData{}
	for key, raw := range n.Attributes {
		parts := strings.Split(key, "/")
		if len(parts) != 3 {
			continue
		}
		ep, err1 := strconv.ParseUint(parts[0], 10, 32)
		cl, err2 := strconv.ParseUint(parts[1], 10, 32)
		at, err3 := strconv.ParseUint(parts[2], 10, 32)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		d := eps[uint32(ep)]
		if d == nil {
			d = &epData{clusters: map[uint32]bool{}, attrs: map[[2]uint32]json.RawMessage{}}
			eps[uint32(ep)] = d
		}
		d.clusters[uint32(cl)] = true
		d.attrs[[2]uint32{uint32(cl), uint32(at)}] = raw
	}
	text := func(raw json.RawMessage) string {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return strings.TrimSpace(s)
		}
		return ""
	}
	if root := eps[rootEndpoint]; root != nil {
		out.Vendor = text(root.attrs[[2]uint32{ClusterBasicInformation, attrBasicVendorName}])
		out.Product = text(root.attrs[[2]uint32{ClusterBasicInformation, attrBasicProductName}])
		out.Label = text(root.attrs[[2]uint32{ClusterBasicInformation, attrBasicNodeLabel}])
	}
	ids := make([]uint32, 0, len(eps))
	for id := range eps {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		d := eps[id]
		es := EndpointSummary{ID: id, Clusters: []uint32{}}
		for cl := range d.clusters {
			es.Clusters = append(es.Clusters, cl)
		}
		sort.Slice(es.Clusters, func(i, j int) bool { return es.Clusters[i] < es.Clusters[j] })
		if d.clusters[ClusterBridgedDeviceBasic] {
			es.Label = text(d.attrs[[2]uint32{ClusterBridgedDeviceBasic, attrBasicNodeLabel}])
			if es.Label == "" {
				es.Label = text(d.attrs[[2]uint32{ClusterBridgedDeviceBasic, attrBasicProductName}])
			}
		}
		es.DeviceTypes = deviceTypes(d.attrs[[2]uint32{ClusterDescriptor, attrDescriptorDeviceTypes}])
		for _, st := range Standards {
			raw, ok := d.attrs[[2]uint32{st.Cluster, st.Attribute}]
			if !ok {
				continue
			}
			r := ReadingSummary{Kind: st.Kind, Name: st.Name, Unit: st.Unit, Cluster: st.Cluster, Attribute: st.Attribute}
			if in := Interpret(st.Kind, raw, 1); !in.Missing {
				r.Current = in.Text
			}
			es.Readings = append(es.Readings, r)
		}
		out.Endpoints = append(out.Endpoints, es)
	}
	if out.Endpoints == nil {
		out.Endpoints = []EndpointSummary{}
	}
	return out
}

// deviceTypes reads Descriptor › DeviceTypeList. The server serialises each
// DeviceTypeStruct with its field ids as keys ({"0": 770, "1": 1}); a newer
// one may use field names, so both are accepted.
func deviceTypes(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var list []map[string]any
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	var out []string
	for _, item := range list {
		v, ok := item["0"]
		if !ok {
			v, ok = item["deviceType"]
		}
		n, isNum := v.(float64)
		if !ok || !isNum {
			continue
		}
		id := uint32(n)
		if id == 0x0016 {
			continue // every node's endpoint 0 is a root node; it says nothing
		}
		if name, known := deviceTypeNames[id]; known {
			out = append(out, name)
		} else {
			out = append(out, fmt.Sprintf("Device type 0x%04X", id))
		}
	}
	return out
}
