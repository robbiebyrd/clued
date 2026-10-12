package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/ops"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

func TestResolverErrorsAnswerInTheEnvelope(t *testing.T) {
	palace, err := service.NewPalace(config.Default(), memstore.New(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(ResolvingHandler(func(r *http.Request) (*service.Palace, error) {
		switch r.Header.Get("X-Palace") {
		case "ok":
			return palace, nil
		case "boom":
			return nil, errors.New("database down")
		}
		return nil, &StatusError{Status: http.StatusForbidden, Kind: "forbidden", Message: "not yours"}
	}, ops.All()))
	t.Cleanup(ts.Close)
	for _, path := range []string{"/api/v1/plans", "/api/v1/ops", "/api/v1/events", "/api/v1/schema", "/api/v1/config"} {
		status, env, raw := do(t, "GET", ts.URL+path)
		if status != http.StatusForbidden || env.Error == nil || env.Error.Kind != "forbidden" || env.Error.Message != "not yours" {
			t.Errorf("%s: %d %s", path, status, raw)
		}
	}
	if status, env, raw := do(t, "GET", ts.URL+"/api/v1/plans", "", "X-Palace", "boom"); status != 500 || env.Error == nil || env.Error.Kind != service.KindStorage {
		t.Errorf("plain error: %d %s", status, raw)
	}
	if status, _, raw := do(t, "GET", ts.URL+"/api/v1/plans", "", "X-Palace", "ok"); status != 200 {
		t.Errorf("resolved: %d %s", status, raw)
	}
}

func TestStatusErrorIsAnError(t *testing.T) {
	err := error(&StatusError{Status: 400, Kind: "invalid", Message: "missing project"})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 400 || err.Error() != "invalid: missing project" {
		t.Errorf("StatusError: %v", err)
	}
}
