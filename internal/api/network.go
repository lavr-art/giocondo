package api

import (
	"net/http"
	"strconv"

	"github.com/artemlavr/giocondo/internal/admin"
)

func (h *Handler) listAddresses(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := h.Admin.ListAddresses(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, out)
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
	out, err := h.Admin.AddAddress(r.Context(), id, in.IP)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 201, out)
}
func (h *Handler) deleteAddress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	aid, err := strconv.ParseInt(r.PathValue("address"), 10, 64)
	if err != nil || aid <= 0 {
		writeError(w, 400, "invalid address ID")
		return
	}
	if err = h.Admin.DeleteAddress(r.Context(), id, aid); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) previewDNS(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := h.Admin.PreviewDNS(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, out)
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
	out, err := h.Admin.EnableDNS(r.Context(), id, in.Confirm)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (h *Handler) rotateIP(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in admin.RotateIPInput
	if !decode(w, r, &in) {
		return
	}
	out, err := h.Admin.RotateIP(r.Context(), id, in)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 202, out)
}
func (h *Handler) resumeIP(w http.ResponseWriter, r *http.Request) {
	var in admin.ResumeIPInput
	if !decode(w, r, &in) {
		return
	}
	out, err := h.Admin.ResumeIP(r.Context(), r.PathValue("id"), r.PathValue("action"), in)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, 202, out)
}
