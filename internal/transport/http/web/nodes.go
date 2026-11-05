package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/pkg/logger"
)

type nodesData struct {
	Nodes []panel.NodeView
}

func (h *Handler) nodesPage(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.Queries.ListNodes(r.Context())
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.render(
		w, r,
		pageNodes,
		page{Title: "Nodes", Nav: pageNodes, Authed: true, Flash: r.URL.Query().Get("flash"), Data: nodesData{Nodes: nodes}},
		http.StatusOK,
	)
}

func formPort(r *http.Request, key string) (uint16, error) {
	s := strings.TrimSpace(r.FormValue(key))
	if s == "" {
		return 0, nil
	}

	p, err := strconv.ParseUint(s, 10, 16)
	if err != nil || p == 0 {
		return 0, fmt.Errorf("%w: port must be a number from 1 to 65535", panel.ErrInvalid)
	}

	return uint16(p), nil
}

func (h *Handler) createNode(w http.ResponseWriter, r *http.Request) {
	in := panel.CreateNodeInput{
		Name:   r.FormValue("name"),
		Host:   r.FormValue("host"),
		IP:     r.FormValue("ip"),
		Region: r.FormValue("region"),
	}

	var err error

	port := func(key string) uint16 {
		if err != nil {
			return 0
		}

		var p uint16

		p, err = formPort(r, key)

		return p
	}

	if r.FormValue("vless") == "on" {
		in.VLESS = &panel.VLESSInput{Port: port("vless_port"), Dest: strings.TrimSpace(r.FormValue("vless_dest"))}
	}

	if r.FormValue("hy2") == "on" {
		in.Hysteria2 = &panel.Hy2Input{Port: port("hy2_port"), ACME: r.FormValue("hy2_acme") == "on",
			ACMEEmail: strings.TrimSpace(r.FormValue("hy2_email")), Obfs: r.FormValue("hy2_obfs") == "on"}
	}

	if r.FormValue("wg") == "on" {
		in.WireGuard = &panel.WGNodeInput{Port: port("wg_port")}
	}

	if err != nil {
		h.fail(w, r, err)

		return
	}

	n, err := h.Nodes.CreateNode(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.redirect(w, r, fmt.Sprintf("/nodes/%d?flash=%s", n.ID, url.QueryEscape("Node added. Install the agent on it.")))
}

type nodeData struct {
	Node      panel.NodeView
	AgentAddr string
	AptRepo   string
	DeployKey string         // panel public SSH key
	Job       *panel.JobView // installation job from ?job=
}

func (h *Handler) nodePage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	n, err := h.Queries.GetNode(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	d := nodeData{Node: n, AgentAddr: h.Provisioning.AgentAddress(), AptRepo: h.Provisioning.AptRepoURL()}
	if n.Runtime != string(panel.RuntimeLocal) {
		if key, err := h.Provisioning.DeployKey(r.Context()); err == nil {
			d.DeployKey = key.AuthorizedKey
		} else {
			logger.FromCtx(r.Context()).Warn("deploy key", "err", err)
		}
	}

	if jobID := r.URL.Query().Get("job"); jobID != "" {
		if j, err := h.Provisioning.Job(r.Context(), jobID); err == nil && j.NodeID == n.ID {
			d.Job = &j
		}
	}

	h.render(
		w, r,
		"node",
		page{Title: n.Name, Nav: pageNodes, Authed: true, Flash: r.URL.Query().Get("flash"), Data: d},
		http.StatusOK,
	)
}

func (h *Handler) nodeCloudInit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	data, name, err := h.Provisioning.CloudInit(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	w.Header().Set("Content-Type", "text/cloud-config; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	_, _ = w.Write(data)
}

func (h *Handler) nodeProvisionSSH(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	in := panel.SSHInput{
		Host: r.FormValue("host"), User: r.FormValue("user"), Password: r.FormValue("password"),
		PrivateKey: r.FormValue("private_key"), HostKeySHA256: r.FormValue("host_key"),
		NoFirewall: r.FormValue("firewall") == "", KeepKey: r.FormValue("remove_key") == "",
	}
	if p := strings.TrimSpace(r.FormValue("port")); p != "" {
		if in.Port, err = strconv.Atoi(p); err != nil {
			h.fail(w, r, fmt.Errorf("%w: port must be a number", panel.ErrInvalid))

			return
		}
	}

	job, err := h.Provisioning.ProvisionSSH(r.Context(), id, in)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.redirect(w, r, fmt.Sprintf("/nodes/%d?job=%s", id, url.QueryEscape(job.ID)))
}

// jobPartial is the job block polled by htmx (every 2 s while the job runs).
func (h *Handler) jobPartial(w http.ResponseWriter, r *http.Request) {
	j, err := h.Provisioning.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.renderPartial(w, r, "node", "job", j)
}

func (h *Handler) nodeBundle(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	data, name, err := h.Provisioning.AgentBundle(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	_, _ = w.Write(data)
}

func (h *Handler) nodeAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	var flash string

	switch r.PathValue("action") {
	case "add-address", "delete-address", "enable-dns", "rotate-ip":
		h.nodeNetwork(w, r)

		return
	case actionEnable, actionDisable:
		off := r.PathValue("action") == actionDisable
		_, err = h.Nodes.UpdateNode(r.Context(), id, panel.UpdateNodeInput{Disabled: &off})
		flash = map[bool]string{true: "Node disabled and removed from subscriptions", false: "Node enabled"}[off]
	case "update":
		host, ip, region := r.FormValue("host"), r.FormValue("ip"), r.FormValue("region")
		_, err = h.Nodes.UpdateNode(r.Context(), id, panel.UpdateNodeInput{Host: &host, IP: &ip, Region: &region})
		flash = "Saved"
	case "delete":
		if err := h.Nodes.DeleteNode(r.Context(), id); err != nil {
			h.fail(w, r, err)

			return
		}

		h.redirect(w, r, "/nodes?flash="+url.QueryEscape("Node removed from the panel"))

		return
	default:
		http.NotFound(w, r)

		return
	}

	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.redirect(w, r, fmt.Sprintf("/nodes/%d?flash=%s", id, url.QueryEscape(flash)))
}
