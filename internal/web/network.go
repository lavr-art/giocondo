package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/artemlavr/giocondo/internal/admin"
)

func (h *Handler) nodeNetwork(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	action := r.PathValue("action")
	switch action {
	case "add-address":
		_, err = h.admin.AddAddress(r.Context(), id, r.FormValue("ip"))
	case "delete-address":
		var aid int64
		aid, err = strconv.ParseInt(r.FormValue("address_id"), 10, 64)
		if err == nil {
			err = h.admin.DeleteAddress(r.Context(), id, aid)
		}
	case "enable-dns":
		_, err = h.admin.EnableDNS(r.Context(), id, r.FormValue("confirm") == "on")
	case "rotate-ip":
		in := admin.RotateIPInput{Mode: r.FormValue("mode"), IP: r.FormValue("ip")}
		if in.Mode == "ssh" {
			target := admin.SSHInput{Host: r.FormValue("host"), User: r.FormValue("user"), Password: r.FormValue("password"), PrivateKey: r.FormValue("private_key"), HostKeySHA256: r.FormValue("host_key"), KeepKey: r.FormValue("keep_key") == "on"}
			if raw := r.FormValue("port"); raw != "" {
				target.Port, err = strconv.Atoi(raw)
			}
			in.SSH = &target
			source := admin.SSHInput{Host: r.FormValue("source_host"), User: r.FormValue("source_user"), Password: r.FormValue("source_password"), PrivateKey: r.FormValue("source_key"), HostKeySHA256: r.FormValue("source_host_key")}
			in.SourceSSH = &source
		}
		if err == nil {
			op, e := h.admin.RotateIP(r.Context(), id, in)
			err = e
			if err == nil {
				h.redirect(w, r, fmt.Sprintf("/nodes/%d?job=%s", id, url.QueryEscape(op.ID)), http.StatusSeeOther)
				return
			}
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, fmt.Sprintf("/nodes/%d", id), http.StatusSeeOther)
}
func (h *Handler) resumeNetwork(w http.ResponseWriter, r *http.Request) {
	in := admin.ResumeIPInput{SourceSSH: &admin.SSHInput{Host: r.FormValue("source_host"), User: r.FormValue("source_user"), Password: r.FormValue("source_password"), PrivateKey: r.FormValue("source_key"), HostKeySHA256: r.FormValue("source_host_key")}}
	op, err := h.admin.ResumeIP(r.Context(), r.PathValue("id"), r.PathValue("action"), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, fmt.Sprintf("/nodes/%d?job=%s", op.NodeID, url.QueryEscape(op.ID)), http.StatusSeeOther)
}
