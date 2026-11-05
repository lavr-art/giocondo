package web

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

func (h *Handler) createHost(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	port, err := formPort(r, "port")
	if err != nil {
		h.fail(w, r, err)

		return
	}

	sort, err := parseDays(r.FormValue("sort")) // an integer from 0
	if err != nil {
		h.fail(w, r, fmt.Errorf("%w: order must be an integer from 0", panel.ErrInvalid))

		return
	}

	_, err = h.Hosts.CreateHost(r.Context(), id, panel.HostInput{
		Inbound: r.FormValue("inbound"), Address: r.FormValue("address"),
		Port: port, SNI: r.FormValue("sni"), Sort: sort,
	})
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.redirect(w, r, fmt.Sprintf("/nodes/%d?flash=%s", id, url.QueryEscape("Address added to the subscription")))
}

func (h *Handler) deleteHost(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	nodeID, err := h.Hosts.DeleteHost(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.redirect(w, r, fmt.Sprintf("/nodes/%d?flash=%s", nodeID, url.QueryEscape("Address removed from the subscription")))
}
