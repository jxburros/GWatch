package checks

import "github.com/jxburros/GWatch/internal/model"

// Templates returns the built-in node templates. Every value is a starting
// point the user can change before saving.
func Templates() []model.NodeTemplate {
	mk := func(t model.CheckType, name string, cfg model.CheckConfig) model.Check {
		return model.Check{
			Type:             t,
			Name:             name,
			Enabled:          true,
			IntervalSeconds:  60,
			TimeoutSeconds:   10,
			Retries:          1,
			FailureThreshold: 2,
			Config:           cfg,
		}
	}
	node := func(name, group, template string) model.Node {
		return model.Node{
			Name:       name,
			Group:      group,
			Tags:       []string{},
			Importance: model.ImportanceNormal,
			Enabled:    true,
			Template:   template,
		}
	}
	return []model.NodeTemplate{
		{
			ID:          "website",
			Name:        "Website",
			Description: "Keep an eye on a website or web app: is it loading, is it fast, and is its security certificate still valid?",
			Icon:        "globe",
			Node:        node("My website", "Websites", "website"),
			Checks: []model.Check{
				mk(model.CheckHTTP, "Website loads", model.CheckConfig{ExpectedStatus: "200-399", CertWarnDays: 14}),
				mk(model.CheckCert, "Certificate", model.CheckConfig{Port: 443, CertWarnDays: 14}),
				mk(model.CheckDNS, "Name resolves", model.CheckConfig{RecordType: "A"}),
			},
		},
		{
			ID:          "home-server",
			Name:        "Home server",
			Description: "A NAS, media box or home server: check that it answers on the network, that remote login (SSH) is open, and that its web page loads.",
			Icon:        "server",
			Node:        node("Home server", "Home", "home-server"),
			Checks: []model.Check{
				mk(model.CheckPing, "Reachable (ping)", model.CheckConfig{PingCount: 4}),
				mk(model.CheckTCP, "SSH port 22", model.CheckConfig{Port: 22}),
				mk(model.CheckHTTP, "Web interface", model.CheckConfig{ExpectedStatus: "200-399", IgnoreTLSErrors: true}),
			},
		},
		{
			ID:          "router",
			Name:        "Router",
			Description: "Your internet router or gateway: check that it answers, that it can look up names on the internet, and that its admin page is up.",
			Icon:        "router",
			Node:        node("Router", "Network", "router"),
			Checks: []model.Check{
				mk(model.CheckPing, "Reachable (ping)", model.CheckConfig{PingCount: 4}),
				mk(model.CheckDNS, "Internet name lookup", model.CheckConfig{Target: "google.com", RecordType: "A"}),
				mk(model.CheckTCP, "Admin page port 80", model.CheckConfig{Port: 80}),
			},
		},
		{
			ID:          "api",
			Name:        "API endpoint",
			Description: "A web API or service that returns JSON: check it responds and that a value in the answer is what you expect.",
			Icon:        "api",
			Node:        node("API endpoint", "Services", "api"),
			Checks: []model.Check{
				mk(model.CheckHTTP, "API responds", model.CheckConfig{ExpectedStatus: "200-299", CertWarnDays: 14}),
				mk(model.CheckJSON, "Status value", model.CheckConfig{ExpectedStatus: "200-299", JSONPath: "status", JSONExpected: "ok"}),
			},
		},
		{
			ID:          "tcp-service",
			Name:        "TCP service",
			Description: "Any service that listens on a port, such as Plex (32400), a game server or a database: check that it accepts connections.",
			Icon:        "plug",
			Node:        node("TCP service", "Services", "tcp-service"),
			Checks: []model.Check{
				mk(model.CheckTCP, "Port accepts connections", model.CheckConfig{Port: 32400}),
			},
		},
		{
			ID:          "dns",
			Name:        "DNS only",
			Description: "Check that a name still resolves, optionally to the address you expect. Useful for your own domain or a dynamic DNS name.",
			Icon:        "dns",
			Node:        node("DNS name", "Network", "dns"),
			Checks: []model.Check{
				mk(model.CheckDNS, "Name resolves", model.CheckConfig{RecordType: "A"}),
			},
		},
	}
}
