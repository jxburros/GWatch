// Package reports renders self-contained printable availability reports.
package reports

import (
	"bytes"
	"context"
	"fmt"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"html/template"
	"net/mail"
	"slices"
	"sort"
	"strings"
	"time"
)

func Validate(defs []model.ReportDefinition) error {
	if len(defs) > 50 {
		return fmt.Errorf("at most 50 reports are allowed")
	}
	seen := map[string]bool{}
	for _, d := range defs {
		if d.ID == "" || len(d.ID) > 100 || seen[d.ID] || strings.ContainsAny(d.ID, "/\\\x00") {
			return fmt.Errorf("report IDs must be unique and contain 1–100 safe characters")
		}
		seen[d.ID] = true
		if strings.TrimSpace(d.Name) == "" || len(d.Name) > 200 {
			return fmt.Errorf("report name is required (up to 200 characters)")
		}
		if d.Period != "weekly" && d.Period != "monthly" {
			return fmt.Errorf("period must be weekly or monthly")
		}
		if d.TargetAvailability < 0 || d.TargetAvailability > 100 {
			return fmt.Errorf("target availability must be between 0 and 100")
		}
		if len(d.Recipients) > 100 || d.Enabled && len(d.Recipients) == 0 {
			return fmt.Errorf("enabled reports need 1–100 recipients")
		}
		for _, r := range d.Recipients {
			if _, err := mail.ParseAddress(r); err != nil {
				return fmt.Errorf("invalid report recipient")
			}
		}
	}
	return nil
}

// Period returns the previous complete calendar week/month in the service's
// local time zone; AddDate preserves midnight across daylight-saving changes.
func Period(now time.Time, period string) (time.Time, time.Time) {
	now = now.In(time.Local)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if period == "monthly" {
		end = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return end.AddDate(0, -1, 0), end
	}
	days := (int(end.Weekday()) + 6) % 7
	end = end.AddDate(0, 0, -days)
	return end.AddDate(0, 0, -7), end
}
func matches(n model.Node, d model.ReportDefinition) bool {
	group := len(d.Groups) == 0
	for _, g := range d.Groups {
		if n.InGroup(g) {
			group = true
		}
	}
	tag := len(d.Tags) == 0
	for _, t := range d.Tags {
		if slices.Contains(n.Tags, t) {
			tag = true
		}
	}
	return group && tag
}

type row struct {
	Node, Check, Availability, Latency, Target string
	Samples                                    int
	Average                                    float64
}
type cert struct {
	Node, Check, Expires string
	Days                 int
}
type report struct {
	Charts               []latencyChart
	Name, From, To, Zone string
	Rows, Nodes, Slow    []row
	Incidents            []model.Incident
	Certs                []cert
}

func Generate(ctx context.Context, st *store.Store, d model.ReportDefinition, from, to time.Time) (string, error) {
	if !to.After(from) || to.Sub(from) > 366*24*time.Hour {
		return "", fmt.Errorf("choose a range of up to 366 days")
	}
	nodes, err := st.ListNodes(ctx)
	if err != nil {
		return "", err
	}
	incs, err := st.ListIncidents(ctx, "all")
	if err != nil {
		return "", err
	}
	last, err := st.LastResults(ctx)
	if err != nil {
		return "", err
	}
	out := report{Name: d.Name, From: from.In(time.Local).Format(time.RFC1123), To: to.In(time.Local).Format(time.RFC1123), Zone: time.Local.String()}
	if out.Name == "" {
		out.Name = "Availability report"
	}
	selected := map[int64]bool{}
	for _, n := range nodes {
		if !matches(n, d) {
			continue
		}
		selected[n.ID] = true
		var count, fail int
		for _, c := range n.Checks {
			sum, err := st.ReportSummary(ctx, c.ID, from, to)
			if err != nil {
				return "", err
			}
			r := row{Node: n.Name, Check: c.Name, Availability: "—", Latency: "—", Samples: sum.Count}
			count += sum.Count
			fail += sum.Failures
			if sum.Count > 0 {
				r.Availability = fmt.Sprintf("%.3f%%", sum.Availability)
				if d.TargetAvailability > 0 {
					r.Target = "Met"
					if sum.Availability < d.TargetAvailability {
						r.Target = "Below target"
					}
				}
			}
			if sum.AvgMS != nil {
				r.Latency = fmt.Sprintf("%.2f ms", *sum.AvgMS)
				r.Average = *sum.AvgMS
				out.Slow = append(out.Slow, r)
			}
			out.Rows = append(out.Rows, r)
			if v, ok := last[c.ID]; ok && v.Details.Cert != nil {
				info := v.Details.Cert
				if info.DaysRemaining <= 30 {
					out.Certs = append(out.Certs, cert{Node: n.Name, Check: c.Name, Expires: info.NotAfter.Format(time.RFC1123), Days: info.DaysRemaining})
				}
			}
		}
		r := row{Node: n.Name, Availability: "—", Samples: count}
		if count > 0 {
			r.Availability = fmt.Sprintf("%.3f%%", 100*float64(count-fail)/float64(count))
		}
		out.Nodes = append(out.Nodes, r)
	}
	for _, i := range incs {
		if selected[i.NodeID] && i.OpenedAt.Before(to) && (i.ResolvedAt == nil || i.ResolvedAt.After(from)) {
			start, end := i.OpenedAt, to
			if start.Before(from) {
				start = from
			}
			if i.ResolvedAt != nil && i.ResolvedAt.Before(end) {
				end = *i.ResolvedAt
			}
			i.DurationSeconds = end.Sub(start).Seconds()
			out.Incidents = append(out.Incidents, i)
		}
	}
	sort.Slice(out.Slow, func(i, j int) bool { return out.Slow[i].Average > out.Slow[j].Average })
	if len(out.Slow) > 10 {
		out.Slow = out.Slow[:10]
	}
	if d.IncludeLatencyCharts {
		out.Charts, err = groupLatencyCharts(ctx, st, nodes, d, from, to)
		if err != nil {
			return "", err
		}
	}
	var b bytes.Buffer
	err = reportTemplate.Execute(&b, out)
	return b.String(), err
}

var reportTemplate = template.Must(template.New("report").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><title>{{.Name}}</title><style>body{font:16px system-ui,sans-serif;color:#17212b;max-width:1100px;margin:32px auto;padding:16px}table{border-collapse:collapse;width:100%;margin:16px 0 32px}th,td{text-align:left;padding:10px;border-bottom:1px solid #ccd3db}h1,h2{color:#102d48}small{color:#465868}@media print{body{margin:0;max-width:none}tr{break-inside:avoid}h2{break-after:avoid}}</style><h1>{{.Name}}</h1><p>{{.From}} – {{.To}}</p><small>Observed-sample availability; missing samples are not counted as successful. Node totals are weighted across check samples. Retained whole buckets are used where raw data has expired. Calendar boundaries use {{.Zone}}. Use your browser's Print → Save as PDF to save this report.</small><h2>Nodes</h2><table><tr><th>Node</th><th>Availability</th><th>Samples</th></tr>{{range .Nodes}}<tr><td>{{.Node}}</td><td>{{.Availability}}</td><td>{{.Samples}}</td></tr>{{else}}<tr><td>No nodes in this scope.</td></tr>{{end}}</table><h2>Checks</h2><table><tr><th>Node / check</th><th>Availability</th><th>Samples</th><th>Average latency</th><th>Target</th></tr>{{range .Rows}}<tr><td>{{.Node}} / {{.Check}}</td><td>{{.Availability}}</td><td>{{.Samples}}</td><td>{{.Latency}}</td><td>{{.Target}}</td></tr>{{end}}</table><h2>Incidents</h2><table><tr><th>Node</th><th>State</th><th>Opened</th><th>Duration (seconds)</th></tr>{{range .Incidents}}<tr><td>{{.NodeName}}</td><td>{{.State}}</td><td>{{.OpenedAt}}</td><td>{{printf "%.0f" .DurationSeconds}}</td></tr>{{else}}<tr><td>No recorded incidents overlap this range.</td></tr>{{end}}</table><h2>Slowest checks</h2><table>{{range .Slow}}<tr><td>{{.Node}} / {{.Check}}</td><td>{{.Latency}}</td></tr>{{else}}<tr><td>No latency samples.</td></tr>{{end}}</table><h2>Certificates expiring within 30 days (latest reading)</h2><table>{{range .Certs}}<tr><td>{{.Node}} / {{.Check}}</td><td>{{.Days}} days</td><td>{{.Expires}}</td></tr>{{else}}<tr><td>No expiring certificates in the latest readings.</td></tr>{{end}}</table>{{if .Charts}}<h2>Group latency</h2><p>Daily averages from retained local-calendar rollups, weighted by successful samples.</p>{{range .Charts}}<h3>{{.Name}}</h3><p>0 to {{.Maximum}}</p><svg xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Daily average latency" viewBox="0 0 700 180" style="width:100%;max-height:240px"><path d="M20 20V160H680" fill="none" stroke="#718096"/><polyline points="{{.Points}}" fill="none" stroke="#075985" stroke-width="3"/></svg>{{end}}{{end}}</html>`))
