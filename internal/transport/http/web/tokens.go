package web

import (
	"net/http"
	"net/url"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

type tokensData struct {
	Tokens  []panel.TokenView
	Created *panel.TokenView // just issued: its secret is shown once
	Secret  string
}

func (h *Handler) tokensPage(w http.ResponseWriter, r *http.Request) {
	h.renderTokens(w, r, tokensData{}, http.StatusOK)
}

func (h *Handler) renderTokens(w http.ResponseWriter, r *http.Request, d tokensData, status int) {
	list, err := h.Tokens.ListTokens(r.Context())
	if err != nil {
		h.fail(w, r, err)

		return
	}

	d.Tokens = list
	h.render(
		w, r,
		"tokens",
		page{Title: "API tokens", Nav: "tokens", Authed: true, Flash: r.URL.Query().Get("flash"), Data: d},
		status,
	)
}

func (h *Handler) createToken(w http.ResponseWriter, r *http.Request) {
	t, secret, err := h.Tokens.CreateToken(r.Context(), r.FormValue("name"), r.FormValue("scope"))
	if err != nil {
		h.fail(w, r, err)

		return
	}

	// No redirect: the secret exists only in this response.
	h.renderTokens(w, r, tokensData{Created: &t, Secret: secret}, http.StatusOK)
}

func (h *Handler) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	if err := h.Tokens.DeleteToken(r.Context(), id); err != nil {
		h.fail(w, r, err)

		return
	}

	h.redirect(w, r, "/tokens?flash="+url.QueryEscape("Token revoked"))
}
