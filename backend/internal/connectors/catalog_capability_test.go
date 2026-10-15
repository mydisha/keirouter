package connectors

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// servesKind reports whether conn implements the interface the pipeline needs
// for kind.
func servesKind(conn core.Connector, kind core.ServiceKind) bool {
	var ok bool
	switch kind {
	case core.ServiceLLM, core.ServiceImageToText:
		// Image understanding falls back to a chat call with the images.
		ok = true
	case core.ServiceEmbedding:
		_, ok = conn.(core.MediaConnector)
	case core.ServiceImage:
		_, ok = conn.(core.ImageConnector)
	case core.ServiceTTS:
		_, ok = conn.(core.SpeechConnector)
	case core.ServiceSTT:
		_, ok = conn.(core.TranscriptionConnector)
	case core.ServiceVideo:
		_, ok = conn.(core.VideoConnector)
	case core.ServiceSearch:
		_, ok = conn.(core.SearchConnector)
	case core.ServiceFetch:
		_, ok = conn.(core.FetchConnector)
	}
	return ok
}

// TestDrivableProvidersServeEveryClaimedKind keeps the catalog honest: a
// provider the dashboard offers as routable may only advertise service kinds
// its connector can actually serve.
func TestDrivableProvidersServeEveryClaimedKind(t *testing.T) {
	reg := DefaultRegistry()
	for _, p := range Catalog() {
		if !Drivable(p) {
			continue
		}
		conn, err := reg.Get(p.ID)
		if err != nil {
			t.Errorf("%s: drivable but has no connector: %v", p.ID, err)
			continue
		}
		for _, kind := range p.ServiceKinds {
			if !servesKind(conn, kind) {
				t.Errorf("%s claims %q but its %T connector cannot serve it", p.ID, kind, conn)
			}
		}
	}
}

func TestAdapterPendingProvidersAreNotRoutable(t *testing.T) {
	reg := DefaultRegistry()
	for id := range adapterPendingProviders {
		p, ok := SpecByID(id)
		if !ok {
			t.Errorf("%s is listed as adapter-pending but is not in the catalog", id)
			continue
		}
		if Drivable(p) {
			t.Errorf("%s must not be drivable", id)
		}
		if _, err := reg.Get(id); err == nil {
			t.Errorf("%s must not get a connector: the generic one would call OpenAI paths it does not serve", id)
		}
	}
	for _, kind := range []core.ServiceKind{core.ServiceTTS, core.ServiceSTT, core.ServiceImage, core.ServiceSearch} {
		for _, p := range SpecsByKind(kind) {
			if adapterPendingProviders[p.ID] {
				t.Errorf("SpecsByKind(%q) offers adapter-pending provider %s", kind, p.ID)
			}
		}
	}
}
