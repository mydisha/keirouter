// Package httputil provides HTTP security utilities including SSRF protection.
package httputil

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
)

// allowPrivateBaseURL, when true, relaxes ValidateOutboundURL/ValidateBaseURL
// so loopback and RFC1918 hosts pass. Cloud metadata, link-local, unspecified,
// multicast, and non-http(s) schemes are still blocked. Set at startup via
// SetAllowPrivateBaseURL.
var allowPrivateBaseURL atomic.Bool

// SetAllowPrivateBaseURL toggles the private-network allowlist for base/outbound
// URL validation. Off by default.
func SetAllowPrivateBaseURL(allow bool) {
	allowPrivateBaseURL.Store(allow)
}

// ErrSSRFBlocked is returned when a URL is blocked by SSRF protection.
type ErrSSRFBlocked struct {
	Reason string
}

func (e *ErrSSRFBlocked) Error() string {
	return fmt.Sprintf("URL blocked: %s", e.Reason)
}

// ValidateOutboundURL checks if a URL is safe for outbound requests.
// It blocks:
// - Non-HTTP schemes (file://, gopher://, etc.)
// - Private/internal IPs (127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
// - Link-local addresses (169.254.0.0/16, fe80::/10)
// - Cloud metadata endpoints (169.254.169.254, metadata.google.internal)
// - Loopback addresses (::1, 127.0.0.1)
func ValidateOutboundURL(rawURL string) error {
	if rawURL == "" {
		return nil // Empty URLs are handled by callers
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return &ErrSSRFBlocked{Reason: "invalid URL format"}
	}

	// Only allow http/https schemes
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return &ErrSSRFBlocked{Reason: fmt.Sprintf("blocked scheme: %s", u.Scheme)}
	}

	// Extract hostname (handle port)
	hostname := u.Hostname()
	if hostname == "" {
		return &ErrSSRFBlocked{Reason: "empty hostname"}
	}

	// Block cloud metadata endpoints (check before DNS resolution)
	blockedHosts := []string{
		"169.254.169.254",
		"metadata.google.internal",
		"metadata.goog",
		"instance-data",
	}
	for _, h := range blockedHosts {
		if strings.EqualFold(hostname, h) {
			return &ErrSSRFBlocked{Reason: "cloud metadata endpoint blocked"}
		}
	}

	// Check if hostname is an IP address
	ip := net.ParseIP(hostname)
	if ip != nil {
		return validateIP(ip)
	}

	// Hostname is a domain - check for IP-like patterns
	// Block domains that look like IPs (e.g., 0x7f000001)
	if isEncodedIP(hostname) {
		return &ErrSSRFBlocked{Reason: "encoded IP address blocked"}
	}

	// Resolve DNS and validate all resolved IPs
	// This prevents DNS rebinding attacks where a domain resolves to a private IP
	ips, err := net.LookupIP(hostname)
	if err != nil {
		// DNS resolution failed - allow the request to proceed
		// (the connection will fail anyway, and blocking on DNS failure could be abused)
		return nil
	}

	for _, ip := range ips {
		if err := validateIP(ip); err != nil {
			return err
		}
	}

	return nil
}

// validateIP checks if an IP address is safe for outbound requests.
func validateIP(ip net.IP) error {
	relaxed := allowPrivateBaseURL.Load()

	// Block loopback (unless explicitly allowed)
	if ip.IsLoopback() && !relaxed {
		return &ErrSSRFBlocked{Reason: "loopback address blocked"}
	}

	// Block private addresses (unless explicitly allowed)
	if ip.IsPrivate() && !relaxed {
		return &ErrSSRFBlocked{Reason: "private network address blocked"}
	}

	// Block link-local unicast
	if ip.IsLinkLocalUnicast() {
		return &ErrSSRFBlocked{Reason: "link-local address blocked"}
	}

	// Block link-local multicast
	if ip.IsLinkLocalMulticast() {
		return &ErrSSRFBlocked{Reason: "link-local multicast blocked"}
	}

	// Block unspecified (0.0.0.0, ::)
	if ip.IsUnspecified() {
		return &ErrSSRFBlocked{Reason: "unspecified address blocked"}
	}

	// Block multicast
	if ip.IsMulticast() {
		return &ErrSSRFBlocked{Reason: "multicast address blocked"}
	}

	// Additional checks for IPv6
	if ip.To4() == nil {
		// Block IPv6 loopback (::1) unless explicitly allowed
		if ip.Equal(net.IPv6loopback) && !relaxed {
			return &ErrSSRFBlocked{Reason: "IPv6 loopback blocked"}
		}

		// Block IPv6 unspecified (::)
		if ip.Equal(net.IPv6zero) {
			return &ErrSSRFBlocked{Reason: "IPv6 unspecified blocked"}
		}
	}

	return nil
}

// isEncodedIP checks if a hostname looks like an encoded IP address.
func isEncodedIP(hostname string) bool {
	// Block hex-encoded IPs (e.g., 0x7f000001)
	if strings.HasPrefix(hostname, "0x") || strings.HasPrefix(hostname, "0X") {
		return true
	}

	// Block octal-encoded IPs (e.g., 0177.0.0.1)
	if strings.HasPrefix(hostname, "0") && !strings.HasPrefix(hostname, "0x") {
		parts := strings.Split(hostname, ".")
		if len(parts) == 4 {
			for _, p := range parts {
				if strings.HasPrefix(p, "0") && len(p) > 1 {
					return true
				}
			}
		}
	}

	// Block integer IPs (e.g., 2130706433 for 127.0.0.1)
	if len(hostname) > 0 && hostname[0] >= '1' && hostname[0] <= '9' {
		for _, c := range hostname {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true // Pure numeric - likely an integer IP
	}

	return false
}

// ValidateBaseURL validates a base URL for provider accounts.
// This is a stricter validation that also checks for common SSRF patterns.
func ValidateBaseURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}

	// Basic SSRF protection
	if err := ValidateOutboundURL(rawURL); err != nil {
		return err
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return &ErrSSRFBlocked{Reason: "invalid URL format"}
	}

	// Ensure path doesn't contain traversal
	if strings.Contains(u.Path, "..") {
		return &ErrSSRFBlocked{Reason: "path traversal blocked"}
	}

	// Ensure no fragment (could be used for URL confusion)
	if u.Fragment != "" {
		return &ErrSSRFBlocked{Reason: "URL fragment not allowed"}
	}

	return nil
}

// IsSupportedProxyScheme reports whether scheme is a proxy scheme the Go
// transport can dial. socks5h resolves DNS on the proxy side.
func IsSupportedProxyScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "http", "https", "socks5", "socks5h":
		return true
	}
	return false
}

// ValidateProxyURL validates a proxy URL.
// Allows http, https, socks5 and socks5h schemes.
func ValidateProxyURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return &ErrSSRFBlocked{Reason: "invalid proxy URL format"}
	}

	if !IsSupportedProxyScheme(u.Scheme) {
		return &ErrSSRFBlocked{Reason: fmt.Sprintf("blocked proxy scheme: %q", u.Scheme)}
	}

	// Validate the proxy host
	hostname := u.Hostname()
	if hostname == "" {
		return &ErrSSRFBlocked{Reason: "empty proxy hostname"}
	}

	// Block cloud metadata
	if hostname == "169.254.169.254" || hostname == "metadata.google.internal" {
		return &ErrSSRFBlocked{Reason: "cloud metadata endpoint blocked"}
	}

	// Check if it's an IP
	ip := net.ParseIP(hostname)
	if ip != nil {
		return validateIP(ip)
	}

	// For domains, allow (proxy servers are typically on public IPs)
	return nil
}

// ValidateOAuthRedirectURI validates a redirect URI for OAuth local callbacks.
// Unlike ValidateOutboundURL, this explicitly allows localhost and loopback
// addresses (127.0.0.1, ::1) because OAuth callbacks are legitimately directed
// to the local machine. It still blocks private networks, cloud metadata, and
// other dangerous targets.
func ValidateOAuthRedirectURI(rawURL string) error {
	if rawURL == "" {
		return nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return &ErrSSRFBlocked{Reason: "invalid URL format"}
	}

	// Only allow http/https schemes
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return &ErrSSRFBlocked{Reason: fmt.Sprintf("blocked scheme: %s", u.Scheme)}
	}

	hostname := u.Hostname()
	if hostname == "" {
		return &ErrSSRFBlocked{Reason: "empty hostname"}
	}

	// Allow localhost by name
	if strings.EqualFold(hostname, "localhost") {
		return nil
	}

	// Check if hostname is an IP address
	ip := net.ParseIP(hostname)
	if ip != nil {
		// Allow loopback (127.0.0.0/8, ::1)
		if ip.IsLoopback() {
			return nil
		}
		// Block everything else that validateIP would block
		return validateIP(ip)
	}

	// For domain names, resolve and check — allow only if ALL resolved IPs are loopback
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return nil // DNS failure → connection will fail anyway
	}

	allLoopback := true
	for _, ip := range ips {
		if !ip.IsLoopback() {
			allLoopback = false
			break
		}
	}
	if allLoopback {
		return nil
	}

	// Non-loopback domain — fall through to standard SSRF checks
	for _, ip := range ips {
		if err := validateIP(ip); err != nil {
			return err
		}
	}

	return nil
}

// ValidateTokenURI validates a Vertex AI token URI.
// Only allows Google's token endpoint.
func ValidateTokenURI(tokenURI string) error {
	if tokenURI == "" {
		return nil
	}

	allowed := "https://oauth2.googleapis.com/token"
	if tokenURI != allowed {
		return &ErrSSRFBlocked{Reason: "invalid token_uri: only Google's token endpoint is allowed"}
	}

	return nil
}
