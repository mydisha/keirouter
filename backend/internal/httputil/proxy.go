package httputil

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// ProxyFunc returns an http.Transport proxy function that routes every request
// through proxyURL except hosts matched by the comma-separated noProxy list.
// The list is parsed once, not per request.
func ProxyFunc(proxyURL *url.URL, noProxy string) func(*http.Request) (*url.URL, error) {
	rules := ParseNoProxy(noProxy)
	return func(req *http.Request) (*url.URL, error) {
		if rules.Match(req.URL) {
			return nil, nil
		}
		return proxyURL, nil
	}
}

// NoProxyRules is a parsed NO_PROXY list. It follows the conventions shared
// by curl and Go's httpproxy package:
//
//   - "*" bypasses every host.
//   - "example.com" matches example.com and any subdomain.
//   - ".example.com" and "*.example.com" match subdomains only.
//   - "10.0.0.0/8" matches IP literals inside the CIDR.
//   - "host:8080" matches only that port.
//
// Matching is case-insensitive.
type NoProxyRules struct {
	all     bool
	domains []noProxyDomain
	ips     []noProxyIP
	cidrs   []*net.IPNet
}

type noProxyDomain struct {
	name       string // lower-case, no leading dot
	subdomOnly bool
	port       string
}

type noProxyIP struct {
	ip   net.IP
	port string
}

// ParseNoProxy parses a comma-separated NO_PROXY list. Blank entries are
// skipped; whitespace around entries is ignored.
func ParseNoProxy(list string) NoProxyRules {
	var r NoProxyRules
	for _, entry := range strings.Split(list, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if entry == "*" {
			r.all = true
			continue
		}
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			r.cidrs = append(r.cidrs, cidr)
			continue
		}
		host, port := entry, ""
		if h, p, err := net.SplitHostPort(entry); err == nil {
			host, port = h, p
		}
		host = strings.Trim(host, "[]")
		if ip := net.ParseIP(host); ip != nil {
			r.ips = append(r.ips, noProxyIP{ip: ip, port: port})
			continue
		}
		d := noProxyDomain{port: port}
		switch {
		case strings.HasPrefix(host, "*."):
			d.name, d.subdomOnly = host[2:], true
		case strings.HasPrefix(host, "."):
			d.name, d.subdomOnly = host[1:], true
		default:
			d.name = host
		}
		if d.name != "" {
			r.domains = append(r.domains, d)
		}
	}
	return r
}

// Match reports whether u should bypass the proxy.
func (r NoProxyRules) Match(u *url.URL) bool {
	if r.all {
		return true
	}
	if u == nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https", "wss":
			port = "443"
		case "http", "ws":
			port = "80"
		}
	}

	if ip := net.ParseIP(host); ip != nil {
		for _, c := range r.cidrs {
			if c.Contains(ip) {
				return true
			}
		}
		for _, e := range r.ips {
			if e.ip.Equal(ip) && (e.port == "" || e.port == port) {
				return true
			}
		}
		return false
	}

	for _, d := range r.domains {
		if d.port != "" && d.port != port {
			continue
		}
		if !d.subdomOnly && host == d.name {
			return true
		}
		if strings.HasSuffix(host, "."+d.name) {
			return true
		}
	}
	return false
}
