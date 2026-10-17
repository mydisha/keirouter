package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/crypto"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// MonitoringKey is the settings row holding the monitoring targets.
const MonitoringKey = "hosting_monitoring"

// MonitorTarget is one Dokploy monitoring service (dokploy/monitoring, port
// 4500 by default). Instances marks the server that runs customer
// containers, whose per-container metrics are read from it.
type MonitorTarget struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Token     string `json:"-"`
	Instances bool   `json:"instances"`
}

type storedTarget struct {
	MonitorTarget
	TokenSealed *crypto.Sealed `json:"token,omitempty"`
}

// LoadMonitoring reads the monitoring targets.
func LoadMonitoring(ctx context.Context, get func(context.Context, string) (string, error), box SecretBox) ([]MonitorTarget, error) {
	raw, err := get(ctx, MonitoringKey)
	if errors.Is(err, store.ErrNotFound) || (err == nil && raw == "") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st []storedTarget
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return nil, fmt.Errorf("hosting: decode monitoring settings: %w", err)
	}
	out := make([]MonitorTarget, 0, len(st))
	for _, t := range st {
		m := t.MonitorTarget
		if t.TokenSealed != nil {
			if box == nil {
				return nil, errors.New("hosting: monitoring token is sealed but no sealer is configured")
			}
			tok, err := box.OpenString(*t.TokenSealed)
			if err != nil {
				return nil, errors.New("hosting: cannot open a sealed monitoring token (master key mismatch?)")
			}
			m.Token = tok
		}
		out = append(out, m)
	}
	return out, nil
}

// SaveMonitoring persists the targets, sealing each token.
func SaveMonitoring(ctx context.Context, set func(context.Context, string, string) error, box SecretBox, targets []MonitorTarget) error {
	st := make([]storedTarget, 0, len(targets))
	for _, t := range targets {
		s := storedTarget{MonitorTarget: t}
		if t.Token != "" {
			if box == nil {
				return errors.New("hosting: cannot store a monitoring token without a sealer")
			}
			sealed, err := box.SealString(t.Token)
			if err != nil {
				return fmt.Errorf("hosting: seal monitoring token: %w", err)
			}
			s.TokenSealed = &sealed
		}
		st = append(st, s)
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return set(ctx, MonitoringKey, string(raw))
}

// NormalizeTarget trims and validates a target.
func NormalizeTarget(t *MonitorTarget) error {
	t.Name = strings.TrimSpace(t.Name)
	t.URL = strings.TrimRight(strings.TrimSpace(t.URL), "/")
	if t.Name == "" || len(t.Name) > 80 {
		return errors.New("server name is required (max 80 characters)")
	}
	u, err := url.Parse(t.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return fmt.Errorf("monitoring URL %q must be http(s)://host:port", t.URL)
	}
	return nil
}

// ServerMetric is one sample from the monitoring service's /metrics.
type ServerMetric struct {
	Timestamp  string  `json:"timestamp"`
	CPU        float64 `json:"cpu"`
	CPUModel   string  `json:"cpuModel"`
	CPUCores   int32   `json:"cpuCores"`
	OS         string  `json:"os"`
	Distro     string  `json:"distro"`
	MemUsed    float64 `json:"memUsed"`
	MemUsedGB  float64 `json:"memUsedGB"`
	MemTotal   float64 `json:"memTotal"`
	Uptime     uint64  `json:"uptime"`
	DiskUsed   float64 `json:"diskUsed"`
	TotalDisk  float64 `json:"totalDisk"`
	NetworkIn  float64 `json:"networkIn"`
	NetworkOut float64 `json:"networkOut"`
}

// ContainerMetric is one sample from /metrics/containers.
type ContainerMetric struct {
	Timestamp string  `json:"timestamp"`
	CPU       float64 `json:"CPU"`
	Memory    struct {
		Percentage float64 `json:"percentage"`
		Used       float64 `json:"used"`
		Total      float64 `json:"total"`
		UsedUnit   string  `json:"usedUnit"`
		TotalUnit  string  `json:"totalUnit"`
	} `json:"Memory"`
	Network struct {
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		InputUnit  string  `json:"inputUnit"`
		OutputUnit string  `json:"outputUnit"`
	} `json:"Network"`
	BlockIO struct {
		Read      float64 `json:"read"`
		Write     float64 `json:"write"`
		ReadUnit  string  `json:"readUnit"`
		WriteUnit string  `json:"writeUnit"`
	} `json:"BlockIO"`
	Name string `json:"Name"`
}

// MonitorClient reads one monitoring service.
type MonitorClient struct {
	base, token string
	http        *http.Client
}

// NewMonitorClient builds a client for t. Redirects are never followed so
// the bearer token cannot leak to another host.
func NewMonitorClient(t MonitorTarget) *MonitorClient {
	return &MonitorClient{base: t.URL, token: t.Token, http: &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Server returns the last limit server samples, oldest first.
func (c *MonitorClient) Server(ctx context.Context, limit int) ([]ServerMetric, error) {
	var out []ServerMetric
	err := c.get(ctx, "/metrics?limit="+strconv.Itoa(limit), &out)
	return out, err
}

// Container returns the last limit samples of an app's containers.
func (c *MonitorClient) Container(ctx context.Context, appName string, limit int) ([]ContainerMetric, error) {
	var out []ContainerMetric
	q := url.Values{"appName": {appName}, "limit": {strconv.Itoa(limit)}}
	err := c.get(ctx, "/metrics/containers?"+q.Encode(), &out)
	return out, err
}

func (c *MonitorClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("monitoring: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("monitoring: status %d: %s", resp.StatusCode, msg)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("monitoring: decode: %w", err)
	}
	return nil
}
