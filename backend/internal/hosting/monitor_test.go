package hosting

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMonitorClient(t *testing.T) {
	var auth, app string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/metrics":
			_, _ = w.Write([]byte(`[{"timestamp":"t","cpu":5.5,"memUsed":40,"memTotal":8,"diskUsed":30,"totalDisk":100}]`))
		case "/metrics/containers":
			app = r.URL.Query().Get("appName")
			_, _ = w.Write([]byte(`[{"timestamp":"t","CPU":1.5,"Memory":{"percentage":2,"used":50,"usedUnit":"MB"},"Name":"nr-a.1.x"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewMonitorClient(MonitorTarget{URL: srv.URL, Token: "tok"})
	sm, err := c.Server(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, "Bearer tok", auth)
	require.InDelta(t, 5.5, sm[0].CPU, 0.001)
	require.InDelta(t, 8, sm[0].MemTotal, 0.001)
	cm, err := c.Container(context.Background(), "nr-a", 5)
	require.NoError(t, err)
	require.Equal(t, "nr-a", app)
	require.InDelta(t, 50, cm[0].Memory.Used, 0.001)

	bad := NewMonitorClient(MonitorTarget{URL: srv.URL + "/nope", Token: "tok"})
	_, err = bad.Server(context.Background(), 1)
	require.ErrorContains(t, err, "404")
}

func TestNormalizeTarget(t *testing.T) {
	ok := MonitorTarget{Name: " main ", URL: "http://1.2.3.4:4500/"}
	require.NoError(t, NormalizeTarget(&ok))
	require.Equal(t, "http://1.2.3.4:4500", ok.URL)
	require.Equal(t, "main", ok.Name)
	for _, u := range []string{"", "ftp://x", "http://user:pw@x:4500", "x:4500"} {
		bad := MonitorTarget{Name: "a", URL: u}
		require.Error(t, NormalizeTarget(&bad), u)
	}
}
