package reports

import (
	"context"
	"fmt"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"sort"
	"strings"
	"time"
)

type latencyChart struct{ Name, Points, Maximum string }
type latencyBucket struct {
	sum   float64
	count int
}

func groupLatencyCharts(ctx context.Context, st *store.Store, nodes []model.Node, d model.ReportDefinition, from, to time.Time) ([]latencyChart, error) {
	groups := map[string]map[int64]latencyBucket{}
	for _, n := range nodes {
		if !matches(n, d) {
			continue
		}
		names := n.GroupList()
		if len(names) == 0 {
			names = []string{"Ungrouped"}
		}
		for _, c := range n.Checks {
			series, err := st.History(ctx, c, n.Name, store.RangeSpec{Name: "report", Duration: to.Sub(from), Bucket: store.Bucket1d}, to)
			if err != nil {
				return nil, err
			}
			for _, g := range names {
				if len(d.Groups) > 0 && !containsFold(d.Groups, g) {
					continue
				}
				if groups[g] == nil {
					groups[g] = map[int64]latencyBucket{}
				}
				for _, point := range series.Points {
					if point.AvgMS == nil || point.Count <= point.Failures {
						continue
					}
					day := point.Timestamp.Unix()
					b := groups[g][day]
					weight := point.Count - point.Failures
					b.sum += *point.AvgMS * float64(weight)
					b.count += weight
					groups[g][day] = b
				}
			}
		}
	}
	var names []string
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []latencyChart{}
	for _, name := range names {
		days := []int64{}
		max := 0.0
		for day, b := range groups[name] {
			days = append(days, day)
			v := b.sum / float64(b.count)
			if v > max {
				max = v
			}
		}
		if len(days) == 0 {
			continue
		}
		sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
		if max == 0 {
			max = 1
		}
		var points []string
		for _, day := range days {
			b := groups[name][day]
			x := 20 + 660*float64(day-from.Unix())/to.Sub(from).Seconds()
			y := 160 - 140*(b.sum/float64(b.count))/max
			points = append(points, fmt.Sprintf("%.2f,%.2f", x, y))
		}
		out = append(out, latencyChart{Name: name, Points: strings.Join(points, " "), Maximum: fmt.Sprintf("%.2f ms", max)})
	}
	return out, nil
}
func containsFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
