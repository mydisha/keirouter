// Package hosting holds the settings, terms version and helpers for the
// hosted 9router product, which runs customer containers on Dokploy.
package hosting

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// SettingsKey is the settings row holding Settings as JSON.
const SettingsKey = "hosting_settings"

// TermsVersion is the hosting terms version users must accept at checkout.
// Bump it whenever the terms page (/ketentuan/hosting-9router) changes.
const TermsVersion = "2026-10-17"

// PeriodDays is how long one paid order keeps an instance active.
const PeriodDays = 30

// GraceDays is how long an expired instance keeps running before the expiry
// job stops it. A renewal inside the grace window extends from the old end
// date, so the grace days are not free.
const GraceDays = 3

// DefaultPasswordEnv is the env var 9router reads its initial dashboard
// password from.
const DefaultPasswordEnv = "INITIAL_PASSWORD"

// SecretBox seals and opens secrets with the master key.
type SecretBox interface {
	SealString(string) (crypto.Sealed, error)
	OpenString(crypto.Sealed) (string, error)
}

// Settings configures the hosting product and its Dokploy target.
type Settings struct {
	Enabled         bool   `json:"enabled"`
	MonthlyPriceIDR int64  `json:"monthly_price_idr"`
	DokployURL      string `json:"dokploy_url"`
	APIKey          string `json:"-"`
	EnvironmentID   string `json:"environment_id"`
	// RootDomain and ServerID are the pre-multi-node single target. They are
	// read once and folded into Nodes as the "default" node.
	RootDomain    string   `json:"root_domain,omitempty"`
	Images        []string `json:"images"`
	ContainerPort int      `json:"container_port"`
	EnvTemplate   string   `json:"env_template"`
	// ServerID deploys customer apps to a separate Dokploy server so they
	// cannot reach the Dokploy host's own services. Empty uses the host.
	ServerID string `json:"server_id,omitempty"`
	// Nodes are the Dokploy servers that run customer instances.
	Nodes []Node `json:"nodes"`
	// PasswordEnv names the env var that receives each instance's random
	// initial dashboard password.
	PasswordEnv string `json:"password_env"`
	// MemoryLimit and CPULimit are passed to Dokploy as-is (bytes and
	// nano-CPUs, e.g. "536870912" and "1000000000"). Empty means no limit.
	MemoryLimit string `json:"memory_limit"`
	CPULimit    string `json:"cpu_limit"`
	// DataPath is the container path of the instance's persistent volume.
	DataPath string `json:"data_path"`
	// SupportURL is where customers waiting for activation can contact the
	// admin (a Telegram link by default).
	SupportURL string `json:"support_url"`
}

type storedSettings struct {
	Settings
	APIKeySealed *crypto.Sealed `json:"api_key,omitempty"`
}

// DefaultPort is the listen port of the luqmenul/9router-go image.
const DefaultPort = 20130

// DefaultDataPath is where the instance keeps its SQLite DB, JWT secret and
// CLI token. It is always backed by a per-instance Docker volume.
const DefaultDataPath = "/data"

// DefaultSupportURL is the activation contact shown to paid customers.
const DefaultSupportURL = "https://t.me/damnwahhh"

// sharedSecretEnv are env vars that must stay unique per instance. Putting
// one in the shared template would let every customer forge sessions on
// every other customer's instance.
var sharedSecretEnv = []string{"JWT_SECRET"}

// Load reads the settings; a missing row yields defaults.
func Load(ctx context.Context, get func(context.Context, string) (string, error), box SecretBox) (Settings, error) {
	raw, err := get(ctx, SettingsKey)
	if errors.Is(err, store.ErrNotFound) || (err == nil && raw == "") {
		return Settings{ContainerPort: DefaultPort, PasswordEnv: DefaultPasswordEnv, DataPath: DefaultDataPath, SupportURL: DefaultSupportURL}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var st storedSettings
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return Settings{}, fmt.Errorf("hosting: decode settings: %w", err)
	}
	s := st.Settings
	s.foldLegacyNode()
	if st.APIKeySealed != nil {
		if box == nil {
			return Settings{}, errors.New("hosting: api key is sealed but no sealer is configured")
		}
		k, err := box.OpenString(*st.APIKeySealed)
		if err != nil {
			return Settings{}, errors.New("hosting: cannot open the sealed api key (master key mismatch?)")
		}
		s.APIKey = k
	}
	if s.ContainerPort == 0 {
		s.ContainerPort = DefaultPort
	}
	if s.PasswordEnv == "" {
		s.PasswordEnv = DefaultPasswordEnv
	}
	if s.DataPath == "" {
		s.DataPath = DefaultDataPath
	}
	if s.SupportURL == "" {
		s.SupportURL = DefaultSupportURL
	}
	return s, nil
}

// Save persists the settings, sealing the API key.
func Save(ctx context.Context, set func(context.Context, string, string) error, box SecretBox, s Settings) error {
	st := storedSettings{Settings: s}
	if s.APIKey != "" {
		if box == nil {
			return errors.New("hosting: cannot store the api key without a sealer")
		}
		sealed, err := box.SealString(s.APIKey)
		if err != nil {
			return fmt.Errorf("hosting: seal api key: %w", err)
		}
		st.APIKeySealed = &sealed
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return set(ctx, SettingsKey, string(raw))
}

// Normalize trims fields and validates them. It does not check reachability.
func (s *Settings) Normalize() error {
	s.DokployURL = strings.TrimRight(strings.TrimSpace(s.DokployURL), "/")
	s.EnvironmentID = strings.TrimSpace(s.EnvironmentID)
	s.RootDomain = strings.ToLower(strings.Trim(strings.TrimSpace(s.RootDomain), "."))
	s.RootDomain = strings.TrimPrefix(s.RootDomain, "*.")
	s.ServerID = strings.TrimSpace(s.ServerID)
	if s.RootDomain != "" && !validDomain(s.RootDomain) {
		return fmt.Errorf("root_domain %q is not a valid domain", s.RootDomain)
	}
	s.foldLegacyNode()
	if err := s.normalizeNodes(); err != nil {
		return err
	}
	var imgs []string
	seen := map[string]bool{}
	for _, img := range s.Images {
		img = strings.TrimSpace(img)
		if img == "" || seen[img] {
			continue
		}
		if strings.ContainsAny(img, " \t\n") {
			return fmt.Errorf("image %q contains whitespace", img)
		}
		if err := checkImageVersion(img); err != nil {
			return err
		}
		seen[img] = true
		imgs = append(imgs, img)
	}
	s.Images = imgs
	if s.ContainerPort == 0 {
		s.ContainerPort = DefaultPort
	}
	s.PasswordEnv = strings.TrimSpace(s.PasswordEnv)
	if s.PasswordEnv == "" {
		s.PasswordEnv = DefaultPasswordEnv
	}
	if !envNameRe.MatchString(s.PasswordEnv) {
		return fmt.Errorf("password_env %q is not a valid env var name", s.PasswordEnv)
	}
	s.DataPath = strings.TrimRight(strings.TrimSpace(s.DataPath), "/")
	if s.DataPath == "" {
		s.DataPath = DefaultDataPath
	}
	if !strings.HasPrefix(s.DataPath, "/") || strings.Contains(s.DataPath, "..") || strings.ContainsAny(s.DataPath, " \t\n") {
		return errors.New("data_path must be an absolute container path")
	}
	s.SupportURL = strings.TrimSpace(s.SupportURL)
	if s.SupportURL == "" {
		s.SupportURL = DefaultSupportURL
	}
	if u, err := url.Parse(s.SupportURL); err != nil || u.Scheme != "https" || u.Host == "" || len(s.SupportURL) > 500 {
		return errors.New("support_url must be an https:// link")
	}
	for _, line := range strings.Split(s.EnvTemplate, "\n") {
		name, _, _ := strings.Cut(strings.TrimSpace(line), "=")
		for _, banned := range sharedSecretEnv {
			if strings.TrimSpace(name) == banned {
				return fmt.Errorf("remove %s from the env template: it must be unique per instance (the image generates one in the data volume)", banned)
			}
		}
	}
	s.MemoryLimit = strings.TrimSpace(s.MemoryLimit)
	s.CPULimit = strings.TrimSpace(s.CPULimit)
	for name, v := range map[string]string{"memory_limit": s.MemoryLimit, "cpu_limit": s.CPULimit} {
		if v != "" && !digitsRe.MatchString(v) {
			return fmt.Errorf("%s must be a whole number", name)
		}
	}
	if s.DokployURL != "" && !strings.HasPrefix(strings.ToLower(s.DokployURL), "https://") {
		return errors.New("dokploy_url must use https:// so the API key is not sent in clear text")
	}
	if s.ContainerPort < 1 || s.ContainerPort > 65535 {
		return errors.New("container_port must be 1-65535")
	}
	if s.MonthlyPriceIDR < 0 {
		return errors.New("monthly_price_idr must not be negative")
	}
	if s.Enabled {
		switch {
		case s.MonthlyPriceIDR <= 0:
			return errors.New("set monthly_price_idr before enabling hosting")
		case s.DokployURL == "", s.APIKey == "", s.EnvironmentID == "":
			return errors.New("set the Dokploy URL, API key and environment ID before enabling hosting")
		case !s.hasEnabledNode():
			return errors.New("add at least one enabled node before enabling hosting")
		case len(s.Images) == 0:
			return errors.New("add at least one image before enabling hosting")
		}
	}
	return nil
}

func (s Settings) hasEnabledNode() bool {
	for _, n := range s.Nodes {
		if n.Enabled {
			return true
		}
	}
	return false
}

// HasImage reports whether img is in the allowed list.
func (s Settings) HasImage(img string) bool {
	for _, i := range s.Images {
		if i == img {
			return true
		}
	}
	return false
}

// MaskAPIKey shows only the last 4 characters.
func MaskAPIKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 4 {
		return "••••"
	}
	return "••••" + k[len(k)-4:]
}

var (
	envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	digitsRe  = regexp.MustCompile(`^[0-9]+$`)
	semverTag = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)
)

// minSafeVersion is the first 9router release without CVE-2026-63732
// (hardcoded default password chained to remote code execution).
var minSafeVersion = [3]int{0, 4, 60}

// checkImageVersion rejects 9router images whose tag is a version older than
// minSafeVersion. Non-version tags (latest, a digest) are allowed.
func checkImageVersion(img string) error {
	ref := img
	if i := strings.Index(ref, "@"); i >= 0 {
		return nil // pinned digest
	}
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon <= slash {
		return nil
	}
	if !strings.Contains(strings.ToLower(ref[:colon]), "9router") {
		return nil
	}
	m := semverTag.FindStringSubmatch(ref[colon+1:])
	if m == nil {
		return nil
	}
	for i := 0; i < 3; i++ {
		n, _ := strconv.Atoi(m[i+1])
		if n != minSafeVersion[i] {
			if n < minSafeVersion[i] {
				return fmt.Errorf("image %q is older than 9router 0.4.60, which fixes CVE-2026-63732", img)
			}
			return nil
		}
	}
	return nil
}

// RandomPassword returns a 20-character password from an unambiguous
// alphabet.
func RandomPassword() (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 20)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

func validDomain(d string) bool {
	if len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

const subdomainAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// RandomSubdomain returns an 8-character label that starts with a letter.
func RandomSubdomain() (string, error) {
	b := make([]byte, 8)
	for i := range b {
		max := len(subdomainAlphabet)
		if i == 0 {
			max = 26
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
		if err != nil {
			return "", err
		}
		b[i] = subdomainAlphabet[n.Int64()]
	}
	return string(b), nil
}
