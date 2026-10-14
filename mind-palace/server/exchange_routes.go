package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
)

// PathTokenExchange trades an environment key (the bearer) for a
// short-lived token (POST; the key is its only authentication).
const PathTokenExchange = "/api/v1/token/exchange"

// exchangeHandler answers PathTokenExchange: body {"client_name",
// "client_info"}, result auth.Exchanged; 401 for a key that is unknown,
// revoked or not an environment key, 429 past the key's hourly limit.
func exchangeHandler(ex auth.Exchanger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, problem := auth.Bearer(r)
		if key == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			httpapi.WriteError(w, http.StatusUnauthorized, "unauthorized", problem)
			return
		}
		var body struct {
			ClientName string            `json:"client_name"`
			ClientInfo map[string]string `json:"client_info"`
		}
		if !decodeSmall(w, r, &body) {
			return
		}
		got, err := ex.Exchange(context.WithoutCancel(r.Context()), key, body.ClientName, body.ClientInfo)
		switch {
		case errors.Is(err, auth.ErrInvalidKey):
			w.Header().Set("WWW-Authenticate", "Bearer")
			httpapi.WriteError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		case errors.Is(err, auth.ErrRateLimited):
			w.Header().Set("Retry-After", "600")
			httpapi.WriteError(w, http.StatusTooManyRequests, "rate_limited", err.Error())
		case err != nil:
			httpapi.WriteError(w, http.StatusBadGateway, "store", err.Error())
		default:
			httpapi.WriteResult(w, http.StatusOK, got)
		}
	})
}
