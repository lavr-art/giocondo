// Package api is the panel REST API with tokens (Authorization: Bearer, scopes in Route.Scope). Spec: docs/api.yaml.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/tokens"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// Route is a method, a path and the required token scope; the test checks docs/api.yaml against this list.
type Route struct {
	Method, Path string
	Scope        string // tokens.ScopeMonitor | ScopeNodeSync | ScopeAdmin
	handler      func(*Handler, http.ResponseWriter, *http.Request)
}

const (
	monitor  = tokens.ScopeMonitor
	nodeSync = tokens.ScopeNodeSync
	adm      = tokens.ScopeAdmin
)

// Item paths shared by the read, update and delete routes.
const (
	userPath = "/api/users/{id}"
	nodePath = "/api/nodes/{id}"
)

// Routes lists all API routes. Reads need monitor, nodes and host overrides need node-sync, the rest admin.
var Routes = []Route{
	{http.MethodGet, "/api/nodes/{id}/addresses", monitor, (*Handler).listAddresses},
	{http.MethodPost, "/api/nodes/{id}/addresses", adm, (*Handler).addAddress},
	{http.MethodDelete, "/api/nodes/{id}/addresses/{address}", adm, (*Handler).deleteAddress},
	{http.MethodGet, "/api/nodes/{id}/dns", monitor, (*Handler).previewDNS},
	{http.MethodPost, "/api/nodes/{id}/dns", adm, (*Handler).enableDNS},
	{http.MethodPost, "/api/nodes/{id}/rotate-ip", adm, (*Handler).rotateIP},
	{http.MethodPost, "/api/jobs/{id}/{action}", adm, (*Handler).resumeIP},
	{http.MethodGet, "/api/users", monitor, (*Handler).listUsers},
	{http.MethodPost, "/api/users", adm, (*Handler).createUser},
	{http.MethodGet, userPath, monitor, (*Handler).getUser},
	{http.MethodPatch, userPath, adm, (*Handler).updateUser},
	{http.MethodDelete, userPath, adm, (*Handler).deleteUser},
	{http.MethodPost, "/api/users/{id}/extend", adm, (*Handler).extendUser},
	{http.MethodPost, "/api/users/{id}/reset-traffic", adm, (*Handler).resetTraffic},
	{http.MethodPost, "/api/users/{id}/rotate-token", adm, (*Handler).rotateToken},
	{http.MethodGet, "/api/nodes", monitor, (*Handler).listNodes},
	{http.MethodPost, "/api/nodes", nodeSync, (*Handler).createNode},
	{http.MethodGet, nodePath, monitor, (*Handler).getNode},
	{http.MethodPatch, nodePath, nodeSync, (*Handler).updateNode},
	{http.MethodDelete, nodePath, nodeSync, (*Handler).deleteNode},
	{http.MethodPost, "/api/nodes/{id}/agent-cert", nodeSync, (*Handler).agentCert},
	{http.MethodPost, "/api/nodes/{id}/cloud-init", nodeSync, (*Handler).cloudInit},
	{http.MethodPost, "/api/nodes/{id}/provision-ssh", nodeSync, (*Handler).provisionSSH},
	{http.MethodGet, "/api/jobs/{id}", nodeSync, (*Handler).getJob},
	{http.MethodGet, "/api/ssh-key", monitor, (*Handler).sshKey},
	{http.MethodGet, "/api/nodes/{id}/hosts", monitor, (*Handler).listHosts},
	{http.MethodPost, "/api/nodes/{id}/hosts", nodeSync, (*Handler).createHost},
	{http.MethodPut, "/api/hosts/{id}", nodeSync, (*Handler).updateHost},
	{http.MethodDelete, "/api/hosts/{id}", nodeSync, (*Handler).deleteHost},
	{http.MethodGet, "/api/tokens", adm, (*Handler).listTokens},
	{http.MethodPost, "/api/tokens", adm, (*Handler).createToken},
	{http.MethodDelete, "/api/tokens/{id}", adm, (*Handler).deleteToken},
}

// Handler serves /api/. Access is by database tokens (Operations.Authenticate).
type Handler struct {
	Dependencies
}

// Register attaches the routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	for _, r := range Routes {
		mux.Handle(
			r.Method+" "+r.Path,
			h.auth(r.Scope, func(w http.ResponseWriter, req *http.Request) { r.handler(h, w, req) }),
		)
	}
}

func (h *Handler) auth(scope string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")

		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, r, http.StatusUnauthorized, "missing Authorization: Bearer <token>")

			return
		}

		t, err := h.Tokens.Authenticate(r.Context(), strings.TrimSpace(token))
		switch {
		case errors.Is(err, tokens.ErrUnauthorized):
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, r, http.StatusUnauthorized, "invalid API token")

			return
		case err != nil:
			h.fail(w, r, err)

			return
		case !tokens.ScopeAllows(t.Scope, scope):
			writeError(w, r, http.StatusForbidden, "token scope "+t.Scope+" is not enough, need "+scope)

			return
		}

		next(w, r)
	})
}

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	// The status is already sent; a failed write usually means the client went away.
	if err := enc.Encode(v); err != nil {
		logger.FromCtx(r.Context()).Debug("write JSON response", "err", err)
	}
}

func writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	writeJSON(w, r, status, errorBody{Error: msg})
}

// fail turns an operation error into an HTTP response.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, panel.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not found")
	case errors.Is(err, panel.ErrConflict):
		writeError(w, r, http.StatusConflict, "already exists")
	case errors.Is(err, panel.ErrInvalid):
		writeError(w, r, http.StatusBadRequest, err.Error())
	default:
		logger.FromCtx(r.Context()).Error("api", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal error")
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, r, http.StatusBadRequest, "bad id")

		return 0, false
	}

	return id, true
}

// decode reads a JSON body (up to 1 MB, unknown fields are an error). An empty body is allowed.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "bad JSON: "+err.Error())

		return false
	}

	return true
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.Users.ListUsers(r.Context())
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, users)
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var in panel.CreateUserInput
	if !decode(w, r, &in) {
		return
	}

	u, err := h.Users.CreateUser(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusCreated, u)
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	u, err := h.Users.GetUser(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, u)
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var in panel.UpdateUserInput
	if !decode(w, r, &in) {
		return
	}

	u, err := h.Users.UpdateUser(r.Context(), id, in)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, u)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := h.Users.DeleteUser(r.Context(), id); err != nil {
		h.fail(w, r, err)

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

	u, err := h.Users.Extend(r.Context(), id, in.Days)
	if err == nil && in.ResetTraffic {
		u, err = h.Users.ResetTraffic(r.Context(), id)
	}

	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, u)
}

func (h *Handler) resetTraffic(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	u, err := h.Users.ResetTraffic(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, u)
}

func (h *Handler) rotateToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	u, err := h.Users.RotateToken(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, u)
}

func (h *Handler) listNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.Queries.ListNodes(r.Context())
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, nodes)
}

func (h *Handler) createNode(w http.ResponseWriter, r *http.Request) {
	var in panel.CreateNodeInput
	if !decode(w, r, &in) {
		return
	}

	n, err := h.Nodes.CreateNode(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusCreated, n)
}

func (h *Handler) getNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	n, err := h.Queries.GetNode(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, n)
}

func (h *Handler) updateNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var in panel.UpdateNodeInput
	if !decode(w, r, &in) {
		return
	}

	n, err := h.Nodes.UpdateNode(r.Context(), id, in)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, n)
}

func (h *Handler) deleteNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := h.Nodes.DeleteNode(r.Context(), id); err != nil {
		h.fail(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) agentCert(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	c, _, err := h.Provisioning.IssueAgentCert(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, c)
}

func (h *Handler) listHosts(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	hosts, err := h.Hosts.ListHosts(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, hosts)
}

func (h *Handler) createHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var in panel.HostInput
	if !decode(w, r, &in) {
		return
	}

	host, err := h.Hosts.CreateHost(r.Context(), id, in)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusCreated, host)
}

func (h *Handler) updateHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var in panel.HostInput
	if !decode(w, r, &in) {
		return
	}

	host, err := h.Hosts.UpdateHost(r.Context(), id, in)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, host)
}

func (h *Handler) deleteHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if _, err := h.Hosts.DeleteHost(r.Context(), id); err != nil {
		h.fail(w, r, err)

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
	panel.TokenView

	Token string `json:"token"`
}

func (h *Handler) listTokens(w http.ResponseWriter, r *http.Request) {
	list, err := h.Tokens.ListTokens(r.Context())
	if err != nil {
		h.fail(w, r, err)

		return
	}

	if list == nil {
		list = []panel.TokenView{}
	}

	writeJSON(w, r, http.StatusOK, list)
}

func (h *Handler) createToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenRequest
	if !decode(w, r, &req) {
		return
	}

	t, secret, err := h.Tokens.CreateToken(r.Context(), req.Name, req.Scope)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusCreated, createTokenResponse{TokenView: t, Token: secret})
}

func (h *Handler) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := h.Tokens.DeleteToken(r.Context(), id); err != nil {
		h.fail(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) cloudInit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	data, name, err := h.Provisioning.CloudInit(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

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

	var in panel.SSHInput
	if !decode(w, r, &in) {
		return
	}

	job, err := h.Provisioning.ProvisionSSH(r.Context(), id, in)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusAccepted, job)
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := h.Provisioning.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, job)
}

type sshKeyResponse struct {
	AuthorizedKey string `json:"authorized_key"`
}

func (h *Handler) sshKey(w http.ResponseWriter, r *http.Request) {
	key, err := h.Provisioning.DeployKey(r.Context())
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, http.StatusOK, sshKeyResponse{AuthorizedKey: key.AuthorizedKey})
}
