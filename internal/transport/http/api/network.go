package api

import (
	"net/http"
	"strconv"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

func (h *Handler) listAddresses(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	out, err := h.Network.ListAddresses(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, 200, out)
}

func (h *Handler) addAddress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var in struct {
		IP string `json:"ip"`
	}
	if !decode(w, r, &in) {
		return
	}

	out, err := h.Network.AddAddress(r.Context(), id, in.IP)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, 201, out)
}

func (h *Handler) deleteAddress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	aid, err := strconv.ParseInt(r.PathValue("address"), 10, 64)
	if err != nil || aid <= 0 {
		writeError(w, r, 400, "invalid address ID")

		return
	}

	if err = h.Network.DeleteAddress(r.Context(), id, aid); err != nil {
		h.fail(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) previewDNS(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	out, err := h.Queries.PreviewDNS(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, 200, out)
}

func (h *Handler) enableDNS(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var in struct {
		Confirm bool `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return
	}

	out, err := h.Network.EnableDNS(r.Context(), id, in.Confirm)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, 200, out)
}

func (h *Handler) rotateIP(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var in panel.RotateIPInput
	if !decode(w, r, &in) {
		return
	}

	out, err := h.Network.RotateIP(r.Context(), id, in)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, 202, out)
}

func (h *Handler) resumeIP(w http.ResponseWriter, r *http.Request) {
	var in panel.ResumeIPInput
	if !decode(w, r, &in) {
		return
	}

	out, err := h.Network.ResumeIP(r.Context(), r.PathValue("id"), r.PathValue("action"), in)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	writeJSON(w, r, 202, out)
}
