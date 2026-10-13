package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/device"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
)

// Routes of the device login and of the "me" routes (PROTOCOL.md §7.9).
const (
	// PathDevice answers GET with instructions for approving a code; the
	// verification URI of a request points here.
	PathDevice = "/api/v1/device"
	// PathDeviceCode starts a request (POST, no authentication).
	PathDeviceCode = "/api/v1/device/code"
	// PathDeviceToken polls for the outcome (POST, no authentication).
	PathDeviceToken = "/api/v1/device/token"
	// PathDevicePending lists the requests (GET, authenticated).
	PathDevicePending = "/api/v1/device/pending"
	// PathDeviceApprove approves a request (POST, authenticated).
	PathDeviceApprove = "/api/v1/device/approve"
	// PathDeviceDeny denies a request (POST, authenticated).
	PathDeviceDeny = "/api/v1/device/deny"
	// PathMeConfig serves the client settings (GET, authenticated).
	PathMeConfig = "/api/v1/me/config"
	// PathMeCurrentToken revokes the caller's own token (DELETE).
	PathMeCurrentToken = "/api/v1/me/tokens/current"

	// deviceMaxBody bounds the small JSON bodies of these routes.
	deviceMaxBody = 64 << 10
)

// deviceRoutes translates the device flow and the "me" routes to HTTP.
type deviceRoutes struct {
	flow         *device.Flow
	clientConfig func() (map[string]any, time.Time)
}

// public registers the unauthenticated routes on mux.
func (d *deviceRoutes) public(mux *http.ServeMux) {
	if d.flow == nil {
		return
	}
	mux.HandleFunc("POST "+PathDeviceCode, d.start)
	mux.HandleFunc("POST "+PathDeviceToken, d.poll)
	mux.HandleFunc("GET "+PathDevice, d.page)
}

// protected registers the authenticated routes on mux.
func (d *deviceRoutes) protected(mux *http.ServeMux) {
	if d.flow != nil {
		mux.HandleFunc("GET "+PathDevicePending, d.pending)
		mux.HandleFunc("POST "+PathDeviceApprove, d.approve)
		mux.HandleFunc("POST "+PathDeviceDeny, d.deny)
		mux.HandleFunc("DELETE "+PathMeCurrentToken, d.revokeCurrent)
	}
	if d.clientConfig != nil {
		mux.HandleFunc("GET "+PathMeConfig, d.config)
	}
}

func (d *deviceRoutes) start(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ClientName string            `json:"client_name"`
		ClientInfo map[string]string `json:"client_info"`
	}
	if !decodeSmall(w, r, &in) {
		return
	}
	st, err := d.flow.Start(in.ClientName, in.ClientInfo)
	if err != nil {
		if errors.Is(err, device.ErrTooMany) {
			httpapi.WriteError(w, http.StatusTooManyRequests, "too_many", "too many login requests are waiting; try again later")
			return
		}
		httpapi.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	base := requestBase(r)
	httpapi.WriteResult(w, http.StatusOK, map[string]any{
		"device_code":               st.DeviceCode,
		"user_code":                 st.UserCode,
		"verification_uri":          base + PathDevice,
		"verification_uri_complete": base + PathDevice + "?user_code=" + url.QueryEscape(st.UserCode),
		"expires_in":                st.ExpiresIn,
		"interval":                  st.Interval,
	})
}

func (d *deviceRoutes) poll(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DeviceCode string `json:"device_code"`
	}
	if !decodeSmall(w, r, &in) {
		return
	}
	link, err := d.flow.Poll(in.DeviceCode)
	switch {
	case err == nil:
		httpapi.WriteResult(w, http.StatusOK, link)
	case errors.Is(err, device.ErrPending):
		httpapi.WriteError(w, http.StatusBadRequest, device.ErrPending.Error(), "the code has not been approved yet")
	case errors.Is(err, device.ErrSlowDown):
		httpapi.WriteError(w, http.StatusBadRequest, device.ErrSlowDown.Error(), "polling too fast")
	case errors.Is(err, device.ErrDenied):
		httpapi.WriteError(w, http.StatusBadRequest, device.ErrDenied.Error(), "the request was denied")
	case errors.Is(err, device.ErrExpired):
		httpapi.WriteError(w, http.StatusBadRequest, device.ErrExpired.Error(), "the code expired before it was approved")
	default:
		httpapi.WriteError(w, http.StatusBadRequest, device.ErrUnknown.Error(), "unknown device code")
	}
}

// page tells a person who opened the verification URI how to approve.
func (d *deviceRoutes) page(w http.ResponseWriter, r *http.Request) {
	code := device.Format(device.Normalize(r.URL.Query().Get("user_code")))
	if code == "" {
		code = "<code>"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "clued server: a machine is asking to log in with the code %s.\n\n"+
		"On a machine already linked to this server, approve it with\n\n"+
		"    mind-palace server device approve %s\n\n"+
		"(or deny it with `server device deny`; `server device list` shows what is waiting).\n"+
		"The plugin's shim runs the same command as `scripts/clued server device approve %s`.\n", code, code, code)
}

func (d *deviceRoutes) pending(w http.ResponseWriter, _ *http.Request) {
	httpapi.WriteResult(w, http.StatusOK, d.flow.Pending())
}

func (d *deviceRoutes) approve(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.FromContext(r.Context())
	if !ok {
		auth.Unauthorized(w, "authentication required")
		return
	}
	var in struct {
		UserCode string   `json:"user_code"`
		Name     string   `json:"name"`
		Projects []string `json:"projects"`
	}
	if !decodeSmall(w, r, &in) {
		return
	}
	approval, err := d.flow.Approve(in.UserCode, p, in.Name, in.Projects)
	if err != nil {
		writeDeviceError(w, err)
		return
	}
	httpapi.WriteResult(w, http.StatusOK, approval)
}

func (d *deviceRoutes) deny(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserCode string `json:"user_code"`
	}
	if !decodeSmall(w, r, &in) {
		return
	}
	if err := d.flow.Deny(in.UserCode); err != nil {
		writeDeviceError(w, err)
		return
	}
	httpapi.WriteResult(w, http.StatusOK, map[string]any{"user_code": device.Format(device.Normalize(in.UserCode)), "denied": true})
}

func (d *deviceRoutes) revokeCurrent(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.FromContext(r.Context())
	if !ok {
		auth.Unauthorized(w, "authentication required")
		return
	}
	if err := d.flow.RevokeToken(p.Name); err != nil {
		httpapi.WriteError(w, http.StatusBadGateway, "store", "token file: "+err.Error())
		return
	}
	httpapi.WriteResult(w, http.StatusOK, map[string]any{"revoked": p.Name, "current": true})
}

func (d *deviceRoutes) config(w http.ResponseWriter, _ *http.Request) {
	cfg, at := d.clientConfig()
	if cfg == nil {
		cfg = map[string]any{}
	}
	httpapi.WriteResult(w, http.StatusOK, map[string]any{"client_config": cfg, "updated_at": at.UTC().Format(time.RFC3339)})
}

// writeDeviceError maps the flow's failures to the envelope.
func writeDeviceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, device.ErrNoRequest), errors.Is(err, device.ErrExpired):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "no pending login request with that code (it may have expired)")
	case errors.Is(err, device.ErrDecided), errors.Is(err, auth.ErrTokenExists):
		httpapi.WriteError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, device.ErrProject):
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", err.Error())
	default:
		httpapi.WriteError(w, http.StatusBadGateway, "store", "token file: "+err.Error())
	}
}

// decodeSmall reads a small JSON object body; an empty body is an empty
// object. It answers 400 or 413 itself and reports whether to go on.
func decodeSmall(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, deviceMaxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpapi.WriteError(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("request body exceeds %d bytes", deviceMaxBody))
			return false
		}
		httpapi.WriteError(w, http.StatusBadRequest, "invalid", "reading the body: "+err.Error())
		return false
	}
	if strings.TrimSpace(string(body)) == "" {
		return true
	}
	if err := json.Unmarshal(body, v); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// requestBase is the scheme and host a client reached the server by, as
// seen through a reverse proxy's X-Forwarded-Proto and X-Forwarded-Host.
func requestBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if v := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); v == "http" || v == "https" {
		scheme = v
	}
	host := r.Host
	if v := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); v != "" {
		host = v
	}
	return scheme + "://" + host
}
