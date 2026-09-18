package checks

import (
	"fmt"
	"strconv"
	"strings"
)

// StatusMatcher decides whether an HTTP status code satisfies the expectation
// configured on a check.
type StatusMatcher interface {
	Matches(code int) bool
	String() string
}

type statusRange struct{ lo, hi int }

type statusMatcher struct {
	ranges []statusRange
	expr   string
}

func (m *statusMatcher) Matches(code int) bool {
	for _, r := range m.ranges {
		if code >= r.lo && code <= r.hi {
			return true
		}
	}
	return false
}

func (m *statusMatcher) String() string { return m.expr }

// ParseStatusExpr parses an expected-status expression such as "200",
// "200-299", "200,301,302" or "2xx" (items may be mixed and separated by
// commas or spaces). An empty expression means 200-399.
func ParseStatusExpr(expr string) (StatusMatcher, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return &statusMatcher{ranges: []statusRange{{200, 399}}, expr: "200-399"}, nil
	}
	fields := strings.FieldsFunc(expr, func(r rune) bool { return r == ',' || r == ' ' || r == ';' })
	m := &statusMatcher{}
	var parts []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		lower := strings.ToLower(f)
		switch {
		case len(lower) == 3 && strings.HasSuffix(lower, "xx") && lower[0] >= '1' && lower[0] <= '5':
			base := int(lower[0]-'0') * 100
			m.ranges = append(m.ranges, statusRange{base, base + 99})
			parts = append(parts, lower)
		case strings.Contains(f, "-"):
			lohi := strings.SplitN(f, "-", 2)
			lo, err1 := parseCode(lohi[0])
			hi, err2 := parseCode(lohi[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid status range %q (use e.g. 200-299)", f)
			}
			if lo > hi {
				return nil, fmt.Errorf("invalid status range %q: start is greater than end", f)
			}
			m.ranges = append(m.ranges, statusRange{lo, hi})
			parts = append(parts, fmt.Sprintf("%d-%d", lo, hi))
		default:
			code, err := parseCode(f)
			if err != nil {
				return nil, fmt.Errorf("invalid status code %q (use e.g. 200, 200-299, 2xx or 200,301)", f)
			}
			m.ranges = append(m.ranges, statusRange{code, code})
			parts = append(parts, strconv.Itoa(code))
		}
	}
	if len(m.ranges) == 0 {
		return nil, fmt.Errorf("invalid status expression %q", expr)
	}
	m.expr = strings.Join(parts, ",")
	return m, nil
}

func parseCode(s string) (int, error) {
	s = strings.TrimSpace(s)
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if n < 100 || n > 599 {
		return 0, fmt.Errorf("status code %d out of range", n)
	}
	return n, nil
}
