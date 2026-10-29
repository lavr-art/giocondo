// Package api is the panel REST API with tokens (Authorization: Bearer, scopes in Route.Scope). Spec: docs/api.yaml.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/artemlavr/giocondo/internal/admin"
	"github.com/artemlavr/giocondo/internal/store"
)

// Route is a method, a path and the required token scope; the test checks docs/api.yaml against this list.
type Route struct {
	Method, Path string
	Scope        string // admin.ScopeMonitor | ScopeNodeSync | ScopeAdmin
	handler      func(*Handler, http.ResponseWriter, *http.Request)
}

const (
	monitor  = admin.ScopeMonitor
	nodeSync = admin.ScopeNodeSync
	adm      = admin.ScopeAdmin
)

// Routes lists all API routes. Reads need monitor, nodes and host overrides need node-sync, the rest admin.
var Routes = []Route{
	{"GET", "/api/nodes/{id}/addresses", monitor, (*Handler).listAddresses},
	{"POST", "/api/nodes/{id}/addresses", adm, (*Handler).addAddress},
	{"DELETE", "/api/nodes/{id}/addresses/{address}", adm, (*Handler).deleteAddress},
	{"GET", "/api/nodes/{id}/dns", monitor, (*Handler).previewDNS},
	{"POST", "/api/nodes/{id}/dns", adm, (*Handler).enableDNS},
	{"POST", "/api/nodes/{id}/rotate-ip", adm, (*Handler).rotateIP},
	{"POST", "/api/jobs/{id}/{action}", adm, (*Handler).resumeIP},
	{"GET", "/api/users", monitor, (*Handler).listUsers},
	{"POST", "/api/users", adm, (*Handler).createUser},
	{"GET", "/api/users/{id}", monitor, (*Handler).getUser},
	{"PATCH", "/api/users/{id}", adm, (*Handler).updateUser},
	{"DELETE", "/api/users/{id}", adm, (*Handler).deleteUser},
	{"POST", "/api/users/{id}/extend", adm, (*Handler).extendUser},
	{"POST", "/api/users/{id}/reset-traffic", adm, (*Handler).resetTraffic},
	{"POST", "/api/users/{id}/rotate-token", adm, (*Handler).rotateToken},
	{"GET", "/api/nodes", monitor, (*Handler).listNodes},
	{"POST", "/api/nodes", nodeSync, (*Handler).createNode},
	{"GET", "/api/nodes/{id}", monitor, (*Handler).getNode},
	{"PATCH", "/api/nodes/{id}", nodeSync, (*Handler).updateNode},
	{"DELETE", "/api/nodes/{id}", nodeSync, (*Handler).deleteNode},
	{"POST", "/api/nodes/{id}/agent-cert", nodeSync, (*Handler).agentCert},
	{"POST", "/api/nodes/{id}/cloud-init", nodeSync, (*Handler).cloudInit},
	{"POST", "/api/nodes/{id}/provision-ssh", nodeSync, (*Handler).provisionSSH},
	{"GET", "/api/jobs/{id}", nodeSync, (*Handler).getJob},
	{"GET", "/api/ssh-key", monitor, (*Handler).sshKey},
	{"GET", "/api/nodes/{id}/hosts", monitor, (*Handler).listHosts},
	{"POST", "/api/nodes/{id}/hosts", nodeSync, (*Handler).createHost},
	{"PUT", "/api/hosts/{id}", nodeSync, (*Handler).updateHost},
	{"DELETE", "/api/hosts/{id}", nodeSync, (*Handler).deleteHost},
	{"GET", "/api/tokens", adm, (*Handler).listTokens},
	{"POST", "/api/tokens", adm, (*Handler).createToken},
	{"DELETE", "/api/tokens/{id}", adm, (*Handler).deleteToken},
}

// Handler serves /api/. Access is by database tokens (admin.Service.Authenticate).
type Handler struct {
	Admin *admin.Service
	Log   *slog.Logger
}

// Register attaches the routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	for _, r := range Routes {
		mux.Handle(r.Method+" "+r.Path, h.auth(r.Scope, func(w http.ResponseWriter, req *http.Request) { r.handler(h, w, req) }))
	}
}

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) auth(scope string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "missing Authorization: Bearer <token>")
			return
		}
		t, err := h.Admin.Authenticate(r.Context(), strings.TrimSpace(token))
		switch {
		case errors.Is(err, admin.ErrUnauthorized):
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "invalid API token")
			return
		case err != nil:
			h.fail(w, err)
			return
		case !admin.ScopeAllows(t.Scope, scope):
			writeError(w, http.StatusForbidden, "token scope "+t.Scope+" is not enough, need "+scope)
			return
		}
		next(w, r)
	})
}

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// fail turns an operation error into an HTTP response.
func (h *Handler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "already exists")
	case errors.Is(err, admin.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		h.log().Error("api", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad id")
		return 0, false
	}
	return id, true
}

// decode reads a JSON body (up to 1 MB, unknown fields are an error). An empty body is allowed.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return false
	}
	return true
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.Admin.ListUsers(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var in admin.CreateUserInput
	if !decode(w, r, &in) {
		return
	}
	u, err := h.Admin.CreateUser(r.Context(), in)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	u, err := h.Admin.GetUser(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in admin.UpdateUserInput
	if !decode(w, r, &in) {
		return
	}
	u, err := h.Admin.UpdateUser(r.Context(), id, in)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.Admin.DeleteUser(r.Context(), id); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) extendUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Days         int  `json:"days"`
		ResetTraffic bool `json:"reset_traffic"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := h.Admin.Extend(r.Context(), id, in.Days)
	if err == nil && in.ResetTraffic {
		u, err = h.Admin.ResetTraffic(r.Context(), id)
	}
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (h *Handler) resetTraffic(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	u, err := h.Admin.ResetTraffic(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (h *Handler) rotateToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	u, err := h.Admin.RotateToken(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (h *Handler) listNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.Admin.ListNodes(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (h *Handler) createNode(w http.ResponseWriter, r *http.Request) {
	var in admin.CreateNodeInput
	if !decode(w, r, &in) {
		return
	}
	n, err := h.Admin.CreateNode(r.Context(), in)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (h *Handler) getNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	n, err := h.Admin.GetNode(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (h *Handler) updateNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in admin.UpdateNodeInput
	if !decode(w, r, &in) {
		return
	}
	n, err := h.Admin.UpdateNode(r.Context(), id, in)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (h *Handler) deleteNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.Admin.DeleteNode(r.Context(), id); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) agentCert(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, _, err := h.Admin.IssueAgentCert(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handler) listHosts(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	hosts, err := h.Admin.ListHosts(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hosts)
}

func (h *Handler) createHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in admin.HostInput
	if !decode(w, r, &in) {
		return
	}
	host, err := h.Admin.CreateHost(r.Context(), id, in)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, host)
}

func (h *Handler) updateHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in admin.HostInput
	if !decode(w, r, &in) {
		return
	}
	host, err := h.Admin.UpdateHost(r.Context(), id, in)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, host)
}

func (h *Handler) deleteHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := h.Admin.DeleteHost(r.Context(), id); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type createTokenRequest struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

// createTokenResponse is the only response that contains the token itself.
type createTokenResponse struct {
	admin.TokenView
	Token string `json:"token"`
}

func (h *Handler) listTokens(w http.ResponseWriter, r *http.Request) {
	list, err := h.Admin.ListTokens(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	if list == nil {
		list = []admin.TokenView{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Handler) createToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenRequest
	if !decode(w, r, &req) {
		return
	}
	t, secret, err := h.Admin.CreateToken(r.Context(), req.Name, req.Scope)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createTokenResponse{TokenView: t, Token: secret})
}

func (h *Handler) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.Admin.DeleteToken(r.Context(), id); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) cloudInit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	data, name, err := h.Admin.CloudInit(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/cloud-config; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(data)
}

func (h *Handler) provisionSSH(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in admin.SSHInput
	if !decode(w, r, &in) {
		return
	}
	job, err := h.Admin.ProvisionSSH(r.Context(), id, in)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := h.Admin.Job(r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

type sshKeyResponse struct {
	AuthorizedKey string `json:"authorized_key"`
}

func (h *Handler) sshKey(w http.ResponseWriter, r *http.Request) {
	key, err := h.Admin.DeployKey()
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sshKeyResponse{AuthorizedKey: key.AuthorizedKey})
}
