package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mydisha/keirouter/backend/internal/cloudflare"
	"github.com/mydisha/keirouter/backend/internal/dokploy"
	"github.com/mydisha/keirouter/backend/internal/hosting"
	"github.com/mydisha/keirouter/backend/internal/httputil"
	"github.com/mydisha/keirouter/backend/internal/store"
)

// dokployAPI is the Dokploy surface the hosting admin uses. Tests swap in a
// fake through Server.dokployFactory.
type dokployAPI interface {
	CreateApplication(ctx context.Context, name, appName, description, environmentID, serverID string) (dokploy.Application, error)
	SetResources(ctx context.Context, applicationID, memoryLimit, cpuLimit string) error
	CreateVolumeMount(ctx context.Context, applicationID, volumeName, mountPath string) error
	SaveDockerProvider(ctx context.Context, applicationID, image string) error
	SaveEnvironment(ctx context.Context, applicationID, env string) error
	CreateDomain(ctx context.Context, applicationID, host string, port int) (dokploy.Domain, error)
	Deploy(ctx context.Context, applicationID string) error
	Stop(ctx context.Context, applicationID string) error
	Start(ctx context.Context, applicationID string) error
	Delete(ctx context.Context, applicationID string) error
	CheckEnvironment(ctx context.Context, environmentID string) error
	ListServers(ctx context.Context) ([]dokploy.Server, error)
}

// dnsAPI is the slice of the Cloudflare API hosting uses; tests swap in a
// fake through Server.dnsFactory.
type dnsAPI interface {
	Zone(ctx context.Context, zoneID string) (string, error)
	FindA(ctx context.Context, zoneID, name string) (cloudflare.Record, bool, error)
	CreateA(ctx context.Context, zoneID, name, ip, comment string) (cloudflare.Record, error)
	Delete(ctx context.Context, zoneID, recordID string) error
}

func (s *Server) dnsClient(st hosting.Settings) dnsAPI {
	if s.dnsFactory != nil {
		return s.dnsFactory(st.CloudflareToken)
	}
	return cloudflare.NewClient(st.CloudflareToken)
}

func (s *Server) dokployClient(st hosting.Settings) dokployAPI {
	if s.dokployFactory != nil {
		return s.dokployFactory(st.DokployURL, st.APIKey)
	}
	return dokploy.NewClient(st.DokployURL, st.APIKey)
}

// dokployTimeout bounds one admin action against Dokploy. The work runs
// detached from the request so closing the tab mid-way does not leave a
// half-created app behind without a recorded id.
const dokployTimeout = 2 * time.Minute

// staleProvisioning is when a provisioning instance is treated as abandoned
// (the process died mid-way) and may be retried or deleted.
const staleProvisioning = dokployTimeout + time.Minute

func (s *Server) mountHosting(r chi.Router) {
	r.Get("/hosting/settings", s.adminGetHostingSettings)
	r.Put("/hosting/settings", s.adminPutHostingSettings)
	r.Post("/hosting/settings/test", s.adminTestHostingSettings)
	r.Get("/hosting/dokploy/servers", s.adminListDokployServers)
	r.Get("/hosting/nodes", s.adminListHostingNodes)
	r.Get("/hosting/instances", s.adminListHostingInstances)
	r.Post("/hosting/instances/{id}/provision", s.adminProvisionHosting)
	r.Post("/hosting/instances/{id}/stop", s.adminStopHosting)
	r.Post("/hosting/instances/{id}/start", s.adminStartHosting)
	r.Post("/hosting/instances/{id}/delete", s.adminDeleteHosting)
	s.mountHostingCoupons(r)
}

func hostingSettingsView(st hosting.Settings) map[string]any {
	images := st.Images
	if images == nil {
		images = []string{}
	}
	return map[string]any{
		"enabled":                 st.Enabled,
		"dokploy_url":             st.DokployURL,
		"api_key_masked":          hosting.MaskAPIKey(st.APIKey),
		"has_api_key":             st.APIKey != "",
		"environment_id":          st.EnvironmentID,
		"root_domain":             st.RootDomain,
		"cloudflare_zone_id":      st.CloudflareZoneID,
		"cloudflare_token_masked": hosting.MaskAPIKey(st.CloudflareToken),
		"has_cloudflare_token":    st.CloudflareToken != "",
		"nodes":                   nodesOrEmpty(st.Nodes),
		"images":                  images,
		"container_port":          st.ContainerPort,
		"env_template":            st.EnvTemplate,
		"password_env":            st.PasswordEnv,
		"memory_limit":            st.MemoryLimit,
		"cpu_limit":               st.CPULimit,
		"data_path":               st.DataPath,
		"support_url":             st.SupportURL,
		"regions":                 regionsOrEmpty(st.Regions),
		"grace_days":              hosting.GraceDays,
		"terms_version":           hosting.TermsVersion,
		"period_days":             hosting.PeriodDays,
	}
}

func (s *Server) adminGetHostingSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadHostingSettings(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, hostingSettingsView(st))
}

type hostingSettingsRequest struct {
	Enabled          bool             `json:"enabled"`
	DokployURL       string           `json:"dokploy_url"`
	APIKey           *string          `json:"api_key"` // nil or "" keeps the stored key
	ClearAPIKey      bool             `json:"clear_api_key"`
	EnvironmentID    string           `json:"environment_id"`
	RootDomain       string           `json:"root_domain"` // main domain of every instance
	CloudflareZoneID string           `json:"cloudflare_zone_id"`
	CloudflareToken  *string          `json:"cloudflare_token"` // nil or "" keeps the stored token
	ClearCFToken     bool             `json:"clear_cloudflare_token"`
	Nodes            []hosting.Node   `json:"nodes"`
	Images           []string         `json:"images"`
	ContainerPort    int              `json:"container_port"`
	EnvTemplate      string           `json:"env_template"`
	PasswordEnv      string           `json:"password_env"`
	MemoryLimit      string           `json:"memory_limit"`
	CPULimit         string           `json:"cpu_limit"`
	DataPath         string           `json:"data_path"`
	SupportURL       string           `json:"support_url"`
	Regions          []hosting.Region `json:"regions"`
}

func (s *Server) adminPutHostingSettings(w http.ResponseWriter, r *http.Request) {
	var body hostingSettingsRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	cur, err := s.loadHostingSettings(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	st := hosting.Settings{
		Enabled:    body.Enabled,
		DokployURL: body.DokployURL, APIKey: cur.APIKey, EnvironmentID: body.EnvironmentID,
		RootDomain: body.RootDomain, CloudflareZoneID: body.CloudflareZoneID, CloudflareToken: cur.CloudflareToken,
		Images: body.Images, ContainerPort: body.ContainerPort,
		EnvTemplate: body.EnvTemplate, PasswordEnv: body.PasswordEnv, Nodes: body.Nodes,
		MemoryLimit: body.MemoryLimit, CPULimit: body.CPULimit, DataPath: body.DataPath,
		SupportURL: body.SupportURL, Regions: body.Regions,
	}
	newKey := body.APIKey != nil && strings.TrimSpace(*body.APIKey) != ""
	if body.ClearAPIKey {
		st.APIKey = ""
	}
	if newKey {
		st.APIKey = strings.TrimSpace(*body.APIKey)
	}
	newToken := body.CloudflareToken != nil && strings.TrimSpace(*body.CloudflareToken) != ""
	if body.ClearCFToken {
		st.CloudflareToken = ""
	}
	if newToken {
		st.CloudflareToken = strings.TrimSpace(*body.CloudflareToken)
	}
	if err := st.Normalize(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.checkRemovedRegions(r.Context(), cur, st); err != nil {
		var inUse regionInUseError
		if errors.As(err, &inUse) {
			writeError(w, http.StatusBadRequest, inUse.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	// The stored key only ever goes to the URL it was entered for. Pointing
	// the settings at a new URL requires typing the key again, so a stolen
	// dashboard session cannot redirect the sealed key to another host.
	if st.APIKey != "" && !newKey && st.DokployURL != cur.DokployURL {
		writeError(w, http.StatusBadRequest, "re-enter the Dokploy API key when changing the Dokploy URL")
		return
	}
	// Same rule for the Cloudflare token: it stays with the zone it was
	// entered for.
	if st.CloudflareToken != "" && !newToken && st.CloudflareZoneID != cur.CloudflareZoneID {
		writeError(w, http.StatusBadRequest, "re-enter the Cloudflare API token when changing the zone ID")
		return
	}
	if st.DokployURL != "" {
		if err := httputil.ValidateBaseURL(st.DokployURL); err != nil {
			writeError(w, http.StatusBadRequest, "dokploy_url: "+err.Error())
			return
		}
	}
	if len(st.EnvTemplate) > 16<<10 {
		writeError(w, http.StatusBadRequest, "env_template is too long")
		return
	}
	if err := hosting.Save(r.Context(), s.settingsSet(), s.financeBox(), st); err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, hostingSettingsView(st))
}

// adminTestHostingSettings checks the stored Dokploy URL, key and
// environment id against the live API.
func (s *Server) adminTestHostingSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadHostingSettings(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if st.DokployURL == "" || st.APIKey == "" {
		writeError(w, http.StatusBadRequest, "save the Dokploy URL and API key first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.dokployClient(st).CheckEnvironment(ctx, st.EnvironmentID); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Dokploy: " + err.Error()})
		return
	}
	if st.CloudflareZoneID != "" && st.CloudflareToken != "" {
		zone, err := s.dnsClient(st).Zone(ctx, st.CloudflareZoneID)
		if err == nil && !st.InZone(zone) {
			err = fmt.Errorf("main domain %s is not in zone %s", st.RootDomain, zone)
		}
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Cloudflare: " + err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// adminListDokployServers lists the remote Dokploy servers so the admin can
// pick a node's server instead of hunting for its id in Dokploy.
func (s *Server) adminListDokployServers(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadHostingSettings(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if st.DokployURL == "" || st.APIKey == "" {
		writeError(w, http.StatusBadRequest, "save the Dokploy URL and API key first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	servers, err := s.dokployClient(st).ListServers(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Dokploy: "+err.Error())
		return
	}
	out := make([]map[string]any, 0, len(servers))
	for _, sv := range servers {
		out = append(out, map[string]any{"id": sv.ServerID, "name": sv.Name, "ip": sv.IPAddress, "status": sv.Status})
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": out})
}

// adminHostingView renders one instance for the admin. orders must be this
// instance's own orders.
func (s *Server) adminHostingView(h store.HostingInstance, orders []store.PaymentOrder, emails map[string]string) map[string]any {
	v := map[string]any{
		"id":                h.ID,
		"key_id":            h.KeyID,
		"status":            string(h.Status),
		"image":             h.Image,
		"subdomain":         h.Subdomain,
		"fqdn":              h.FQDN,
		"url":               "",
		"dokploy_app_id":    h.DokployAppID,
		"dokploy_app_name":  h.DokployAppName,
		"dokploy_domain_id": h.DokployDomainID,
		"data_volume":       h.DataVolume,
		"node_id":           h.NodeID,
		"region":            h.Region,
		"last_error":        h.LastError,
		"paid_until":        timeOrEmpty(h.PaidUntil),
		"prepaid_days":      h.PrepaidDays,
		"created_at":        h.CreatedAt,
		"updated_at":        h.UpdatedAt,
		"provisioned_at":    timeOrEmpty(h.ProvisionedAt),
		"has_password":      h.Password.Ciphertext != "",
	}
	if h.FQDN != "" {
		v["url"] = "https://" + h.FQDN
	}
	list := make([]map[string]any, 0, len(orders))
	for _, o := range orders {
		if e, ok := emails[o.GoogleSub]; ok && v["user_email"] == nil {
			v["user_email"] = e
		}
		list = append(list, map[string]any{
			"order_id": o.ID, "status": string(o.Status), "amount_idr": o.AmountIDR,
			"created_at": o.CreatedAt, "paid_at": o.PaidAt,
			"terms_version": o.TermsVersion, "terms_accepted_at": o.TermsAcceptedAt,
			"coupon_code": o.CouponCode, "base_amount_idr": o.BaseAmountIDR, "bonus_days": o.BonusDays,
			"reason": o.Reason,
		})
	}
	v["orders"] = list
	return v
}

func (s *Server) adminListHostingInstances(w http.ResponseWriter, r *http.Request) {
	status := store.HostingStatus(strings.TrimSpace(r.URL.Query().Get("status")))
	instances, err := s.db.Hosting().ListAll(r.Context(), status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	orders, err := s.db.PaymentOrders().ListByProduct(r.Context(), store.ProductHosting)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	byInstance := make(map[string][]store.PaymentOrder, len(instances))
	for _, o := range orders {
		byInstance[o.HostingInstanceID] = append(byInstance[o.HostingInstanceID], o)
	}
	emails := map[string]string{}
	if len(orders) > 0 {
		users, uerr := s.db.PortalUsers().List(r.Context())
		if uerr != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, uerr, "internal server error"))
			return
		}
		for _, u := range users {
			emails[u.GoogleSub] = u.Email
		}
	}
	out := make([]map[string]any, 0, len(instances))
	for _, h := range instances {
		out = append(out, s.adminHostingView(h, byInstance[h.ID], emails))
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": out})
}

// loadHostingInstance reads {id} or writes a 404/500.
func (s *Server) loadHostingInstance(w http.ResponseWriter, r *http.Request) (store.HostingInstance, bool) {
	h, err := s.db.Hosting().Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "instance not found")
		} else {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		}
		return store.HostingInstance{}, false
	}
	return h, true
}

// loadReadyHostingSettings loads settings that can reach Dokploy.
func (s *Server) loadReadyHostingSettings(w http.ResponseWriter, r *http.Request) (hosting.Settings, bool) {
	st, err := s.loadHostingSettings(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return hosting.Settings{}, false
	}
	if st.DokployURL == "" || st.APIKey == "" || st.EnvironmentID == "" || len(st.Nodes) == 0 ||
		st.RootDomain == "" || st.CloudflareZoneID == "" || st.CloudflareToken == "" {
		writeError(w, http.StatusBadRequest, "hosting settings are incomplete: set the Dokploy URL, API key, environment ID, main domain, Cloudflare zone ID and token, and at least one node")
		return hosting.Settings{}, false
	}
	return st, true
}

// adminProvisionHosting creates the instance on Dokploy: application, Docker
// image, environment, domain on a random subdomain, then a deploy. Each id is
// saved as soon as Dokploy returns it, so a retry after a failure resumes at
// the first missing step instead of creating duplicates.
func (s *Server) adminProvisionHosting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Image  string `json:"image"`
		NodeID string `json:"node_id"` // empty picks the node with the most room
		// Overcommit places the instance even when the node (or every node
		// in the region) is full.
		Overcommit bool `json:"overcommit"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	st, ok := s.loadReadyHostingSettings(w, r)
	if !ok {
		return
	}
	image := strings.TrimSpace(body.Image)
	if !st.HasImage(image) {
		writeError(w, http.StatusBadRequest, "image is not in the allowed list")
		return
	}
	h, ok := s.loadHostingInstance(w, r)
	if !ok {
		return
	}
	if s.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "a master key is required to store instance passwords")
		return
	}
	node, status, msg := s.chooseHostingNode(r.Context(), st, h, strings.TrimSpace(body.NodeID), body.Overcommit)
	if status != 0 {
		writeError(w, status, msg)
		return
	}
	from := []store.HostingStatus{store.HostingPaid, store.HostingFailed}
	if h.Status == store.HostingProvisioning && time.Since(h.UpdatedAt) > staleProvisioning {
		from = append(from, store.HostingProvisioning) // a crash left it stuck
	}
	moved, err := s.db.Hosting().Transition(r.Context(), h.ID, from, store.HostingProvisioning, h.LastError)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if !moved {
		writeError(w, http.StatusConflict, "instance is "+string(h.Status)+"; only paid or failed instances can be created")
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), dokployTimeout)
	defer cancel()
	h.Image = image
	if perr := s.provisionOnDokploy(ctx, st, node, &h); perr != nil {
		s.log.Error("hosting: provision failed", "instance", h.ID, "err", perr)
		if _, terr := s.db.Hosting().Transition(ctx, h.ID, []store.HostingStatus{store.HostingProvisioning}, store.HostingFailed, perr.Error()); terr != nil {
			s.log.Error("hosting: mark failed", "instance", h.ID, "err", terr)
		}
		writeError(w, http.StatusBadGateway, "provisioning failed: "+perr.Error())
		return
	}

	// A payment may land while we provision; reload and retry the guarded
	// update so its prepaid days are not lost.
	marked := false
	for attempt := 0; attempt < 5 && !marked; attempt++ {
		cur, gerr := s.db.Hosting().Get(ctx, h.ID)
		if gerr != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, gerr, "internal server error"))
			return
		}
		done, merr := s.db.Hosting().MarkProvisioned(ctx, cur, time.Now())
		if merr != nil {
			writeError(w, http.StatusInternalServerError, sanitizeError(s.log, merr, "internal server error"))
			return
		}
		marked = done
	}
	if !marked {
		writeError(w, http.StatusConflict, "deployed, but the instance changed while saving; reload and retry")
		return
	}
	final, err := s.db.Hosting().Get(ctx, h.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, s.adminHostingView(final, nil, nil))
}

// chooseHostingNode picks where an instance is created. An instance that
// already has a subdomain is tied to its node (the subdomain lives under that
// node's wildcard domain). Otherwise the requested node is used if it is
// enabled and has room, or the node with the most room in the instance's
// region when none is named. overcommit lets a full node take it anyway.
// A non-zero status is an error to return.
func (s *Server) chooseHostingNode(ctx context.Context, st hosting.Settings, h store.HostingInstance, want string, overcommit bool) (hosting.Node, int, string) {
	if h.NodeID != "" && h.Subdomain != "" {
		n, ok := st.Node(h.NodeID)
		if !ok {
			return hosting.Node{}, http.StatusConflict, "this instance's node " + h.NodeID + " was removed from the settings; add it back to retry"
		}
		if want != "" && want != h.NodeID {
			return hosting.Node{}, http.StatusConflict, "this instance is already set up on node " + n.Name + "; retry it there or delete it"
		}
		return n, 0, ""
	}
	used, err := s.db.Hosting().CountByNode(ctx)
	if err != nil {
		return hosting.Node{}, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error")
	}
	loads := st.Loads(used)
	if want == "" {
		// The customer paid for a region: only its nodes are candidates.
		candidates := hosting.InRegion(loads, h.Region)
		pick := hosting.PickNode
		if overcommit {
			pick = hosting.PickNodeOvercommit
		}
		best, ok := pick(candidates)
		if !ok {
			if h.Region != "" {
				return hosting.Node{}, http.StatusConflict, "every node in region " + h.Region + " is full or disabled; pick a node, raise a capacity or overcommit"
			}
			return hosting.Node{}, http.StatusConflict, "every node is full or disabled; add a node, raise a capacity or overcommit"
		}
		return best.Node, 0, ""
	}
	for _, l := range loads {
		if l.ID != want {
			continue
		}
		if !l.Enabled {
			return hosting.Node{}, http.StatusConflict, "node " + l.Name + " is disabled"
		}
		if !l.Available && !overcommit {
			return hosting.Node{}, http.StatusConflict, "node " + l.Name + " is full; overcommit to place it anyway"
		}
		return l.Node, 0, ""
	}
	return hosting.Node{}, http.StatusBadRequest, "unknown node"
}

func regionsOrEmpty(r []hosting.Region) []hosting.Region {
	if r == nil {
		return []hosting.Region{}
	}
	return r
}

func nodesOrEmpty(n []hosting.Node) []hosting.Node {
	if n == nil {
		return []hosting.Node{}
	}
	return n
}

// adminListHostingNodes returns every node with its usage and the node a new
// instance would go to by default.
func (s *Server) adminListHostingNodes(w http.ResponseWriter, r *http.Request) {
	st, err := s.loadHostingSettings(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	used, err := s.db.Hosting().CountByNode(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	loads := st.Loads(used)
	recommended := ""
	if best, ok := hosting.PickNode(loads); ok {
		recommended = best.ID
	}
	waiting, err := s.db.Hosting().CountWaitingByRegion(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": loads, "recommended": recommended, "regions": st.RegionStocks(used, waiting),
	})
}

func (s *Server) provisionOnDokploy(ctx context.Context, st hosting.Settings, node hosting.Node, h *store.HostingInstance) error {
	api := s.dokployClient(st)
	repo := s.db.Hosting()

	if h.Subdomain == "" {
		for i := 0; i < 5 && h.Subdomain == ""; i++ {
			sub, err := hosting.RandomSubdomain()
			if err != nil {
				return err
			}
			taken, err := repo.SubdomainExists(ctx, sub)
			if err != nil {
				return err
			}
			if !taken {
				h.Subdomain = sub
			}
		}
		if h.Subdomain == "" {
			return errors.New("could not pick a free subdomain")
		}
		h.NodeID = node.ID
		h.FQDN = h.Subdomain + "." + st.RootDomain
		if err := repo.SetDokploy(ctx, *h); err != nil {
			return err
		}
	}

	var password string
	if h.Password.Ciphertext == "" {
		pw, err := hosting.RandomPassword()
		if err != nil {
			return err
		}
		sealed, err := s.vault.Sealer().SealString(pw)
		if err != nil {
			return fmt.Errorf("seal password: %w", err)
		}
		h.Password, password = sealed, pw
		if err := repo.SetDokploy(ctx, *h); err != nil {
			return err
		}
	} else {
		pw, err := s.vault.Sealer().OpenString(h.Password)
		if err != nil {
			return fmt.Errorf("open stored password: %w", err)
		}
		password = pw
	}

	if h.DokployAppID == "" {
		app, err := api.CreateApplication(ctx, "9router-"+h.Subdomain, "nr-"+h.Subdomain, s.hostingAppDescription(ctx, h.KeyID), st.EnvironmentID, node.ServerID)
		if err != nil {
			return fmt.Errorf("create application: %w", err)
		}
		h.DokployAppID, h.DokployAppName = app.ApplicationID, app.AppName
		if err := repo.SetDokploy(ctx, *h); err != nil {
			return err
		}
	}
	if h.DataVolume == "" {
		vol := "nr-" + h.Subdomain + "-data"
		if err := api.CreateVolumeMount(ctx, h.DokployAppID, vol, st.DataPath); err != nil {
			return fmt.Errorf("mount data volume: %w", err)
		}
		h.DataVolume = vol
		if err := repo.SetDokploy(ctx, *h); err != nil {
			return err
		}
	}
	if err := api.SaveDockerProvider(ctx, h.DokployAppID, h.Image); err != nil {
		return fmt.Errorf("set docker image: %w", err)
	}
	if err := repo.SetDokploy(ctx, *h); err != nil {
		return err
	}
	if err := api.SaveEnvironment(ctx, h.DokployAppID, buildHostingEnv(st, *h, password)); err != nil {
		return fmt.Errorf("set environment: %w", err)
	}
	if err := api.SetResources(ctx, h.DokployAppID, st.MemoryLimit, st.CPULimit); err != nil {
		return fmt.Errorf("set resource limits: %w", err)
	}
	// Always checked, not only the first time: a record deleted by hand or
	// by a racing delete is recreated instead of leaving the instance
	// without DNS. FindA by name keeps this from making duplicates.
	id, err := s.ensureDNSRecord(ctx, st, node, *h)
	if err != nil {
		return err
	}
	if id != h.DNSRecordID {
		h.DNSRecordID = id
		if err := repo.SetDokploy(ctx, *h); err != nil {
			return err
		}
	}
	if h.DokployDomainID == "" {
		d, err := api.CreateDomain(ctx, h.DokployAppID, h.FQDN, st.ContainerPort)
		if err != nil {
			return fmt.Errorf("create domain: %w", err)
		}
		h.DokployDomainID = d.DomainID
		if h.DokployDomainID == "" {
			h.DokployDomainID = "created" // Dokploy did not echo an id; never create it twice
		}
		if err := repo.SetDokploy(ctx, *h); err != nil {
			return err
		}
	}
	if err := api.Deploy(ctx, h.DokployAppID); err != nil {
		return fmt.Errorf("deploy: %w", err)
	}
	return nil
}

// ensureDNSRecord returns the id of the proxied A record for the instance's
// FQDN, creating it when missing. A record already there is reused when it
// points at the node, and refused when it points elsewhere. The zone must
// hold the main domain, or the record would land under the wrong name.
func (s *Server) ensureDNSRecord(ctx context.Context, st hosting.Settings, node hosting.Node, h store.HostingInstance) (string, error) {
	dns := s.dnsClient(st)
	zone, err := dns.Zone(ctx, st.CloudflareZoneID)
	if err != nil {
		return "", fmt.Errorf("read Cloudflare zone: %w", err)
	}
	if !st.InZone(zone) {
		return "", fmt.Errorf("main domain %q is not in zone %s", st.RootDomain, zone)
	}
	rec, found, err := dns.FindA(ctx, st.CloudflareZoneID, h.FQDN)
	if err != nil {
		return "", fmt.Errorf("find DNS record: %w", err)
	}
	if found {
		if rec.Content != node.PublicIP {
			return "", fmt.Errorf("DNS record %s exists with a different IP (%s)", h.FQDN, rec.Content)
		}
		return rec.ID, nil
	}
	rec, err = dns.CreateA(ctx, st.CloudflareZoneID, h.FQDN, node.PublicIP, "keirouter hosting "+h.ID)
	if err != nil {
		return "", fmt.Errorf("create DNS record: %w", err)
	}
	return rec.ID, nil
}

// buildHostingEnv fills the admin's env template, then sets the variables the
// platform owns: the instance's own random password (never the image default
// or a value shared across customers), the listen port and the data dir that
// the volume backs. STRICT_SSL defaults to on so the image never retries
// upstream calls with TLS verification off; the template may override it.
// Placeholders: {{SUBDOMAIN}}, {{FQDN}}, {{URL}}, {{INSTANCE_ID}}, {{PASSWORD}}.
func buildHostingEnv(st hosting.Settings, h store.HostingInstance, password string) string {
	env := strings.NewReplacer(
		"{{SUBDOMAIN}}", h.Subdomain,
		"{{FQDN}}", h.FQDN,
		"{{URL}}", "https://"+h.FQDN,
		"{{INSTANCE_ID}}", h.ID,
		"{{PASSWORD}}", password,
	).Replace(st.EnvTemplate)
	managed := map[string]string{
		st.PasswordEnv: password,
		"PORT":         strconv.Itoa(st.ContainerPort),
		"DATA_DIR":     st.DataPath,
	}
	order := []string{st.PasswordEnv, "PORT", "DATA_DIR"}
	var lines []string
	seen := map[string]bool{}
	for _, line := range strings.Split(env, "\n") {
		line = strings.TrimRight(line, "\r")
		name, _, _ := strings.Cut(strings.TrimSpace(line), "=")
		name = strings.TrimSpace(name)
		if _, ours := managed[name]; ours {
			continue // ours wins
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		seen[name] = true
		lines = append(lines, line)
	}
	if !seen["STRICT_SSL"] {
		lines = append(lines, "STRICT_SSL=true")
	}
	for _, k := range order {
		lines = append(lines, k+"="+managed[k])
	}
	return strings.Join(lines, "\n")
}

func (s *Server) hostingDokployAction(w http.ResponseWriter, r *http.Request, from []store.HostingStatus, to store.HostingStatus,
	call func(ctx context.Context, api dokployAPI, appID string) error) {
	h, ok := s.loadHostingInstance(w, r)
	if !ok {
		return
	}
	allowed := false
	for _, f := range from {
		if h.Status == f {
			allowed = true
		}
	}
	if h.Status == store.HostingProvisioning && to == store.HostingDeleted && time.Since(h.UpdatedAt) > staleProvisioning {
		allowed = true
		from = append(from, store.HostingProvisioning)
	}
	if !allowed {
		writeError(w, http.StatusConflict, "instance is "+string(h.Status))
		return
	}
	// Only the status that was read may move: a payment that changes the
	// instance mid-call (say expired to active) makes the action fail
	// instead of silently overriding it.
	from = []store.HostingStatus{h.Status}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), dokployTimeout)
	defer cancel()
	// Deleting removes the DNS record first: if Cloudflare fails nothing has
	// changed yet and the admin can simply retry.
	if to == store.HostingDeleted && h.DNSRecordID != "" {
		st, ok := s.loadReadyHostingSettings(w, r)
		if !ok {
			return
		}
		if err := s.dnsClient(st).Delete(ctx, st.CloudflareZoneID, h.DNSRecordID); err != nil {
			s.log.Error("hosting: delete DNS record failed", "instance", h.ID, "err", err)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	if h.DokployAppID != "" {
		st, ok := s.loadReadyHostingSettings(w, r)
		if !ok {
			return
		}
		if err := call(ctx, s.dokployClient(st), h.DokployAppID); err != nil {
			s.log.Error("hosting: dokploy action failed", "instance", h.ID, "to", to, "err", err)
			writeError(w, http.StatusBadGateway, "dokploy: "+err.Error())
			return
		}
	}
	moved, err := s.db.Hosting().Transition(ctx, h.ID, from, to, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	if !moved {
		writeError(w, http.StatusConflict, "instance changed while the action ran; reload and retry")
		return
	}
	updated, err := s.db.Hosting().Get(ctx, h.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, sanitizeError(s.log, err, "internal server error"))
		return
	}
	writeJSON(w, http.StatusOK, s.adminHostingView(updated, nil, nil))
}

func (s *Server) adminStopHosting(w http.ResponseWriter, r *http.Request) {
	s.hostingDokployAction(w, r, []store.HostingStatus{store.HostingActive, store.HostingExpired}, store.HostingStopped,
		func(ctx context.Context, api dokployAPI, id string) error { return api.Stop(ctx, id) })
}

func (s *Server) adminStartHosting(w http.ResponseWriter, r *http.Request) {
	s.hostingDokployAction(w, r, []store.HostingStatus{store.HostingStopped}, store.HostingActive,
		func(ctx context.Context, api dokployAPI, id string) error { return api.Start(ctx, id) })
}

func (s *Server) adminDeleteHosting(w http.ResponseWriter, r *http.Request) {
	s.hostingDokployAction(w, r, []store.HostingStatus{
		store.HostingAwaitingPayment, store.HostingPaid, store.HostingFailed,
		store.HostingActive, store.HostingExpired, store.HostingStopped,
	}, store.HostingDeleted,
		func(ctx context.Context, api dokployAPI, id string) error { return api.Delete(ctx, id) })
}

// checkRemovedRegions refuses to drop a region that live instances still
// belong to: their owners could no longer renew. Deleted instances don't count.
func (s *Server) checkRemovedRegions(ctx context.Context, cur, next hosting.Settings) error {
	removed := map[string]string{}
	for _, rg := range cur.Regions {
		if _, ok := next.Region(rg.ID); !ok {
			removed[rg.ID] = rg.Name
		}
	}
	if len(removed) == 0 {
		return nil
	}
	list, err := s.db.Hosting().ListAll(ctx, "")
	if err != nil {
		return err
	}
	used := map[string]int{}
	for _, h := range list {
		if h.Status == store.HostingDeleted {
			continue
		}
		if id := cur.InstanceRegion(h.Region, h.NodeID); removed[id] != "" {
			used[id]++
		}
	}
	for _, rg := range cur.Regions {
		if n := used[rg.ID]; n > 0 {
			return regionInUseError{name: rg.Name, count: n}
		}
	}
	return nil
}

type regionInUseError struct {
	name  string
	count int
}

func (e regionInUseError) Error() string {
	return fmt.Sprintf("region %s still has %d instance(s); turn off For sale instead of deleting it", e.name, e.count)
}

// hostingAppDescription names the customer in the Dokploy application's
// description, so an app found in Dokploy can be traced back to its owner.
func (s *Server) hostingAppDescription(ctx context.Context, keyID string) string {
	if u, err := s.db.PortalUsers().GetByKey(ctx, keyID); err == nil && u.Email != "" {
		return "9router for " + u.Email + " (keirouter)"
	}
	return "9router for API key " + keyID + " (keirouter)"
}
