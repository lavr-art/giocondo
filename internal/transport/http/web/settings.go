package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

func (h *Handler) settingsPage(w http.ResponseWriter, r *http.Request) {
	s, err := h.Settings.SubscriptionSettings(r.Context())
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.render(
		w, r,
		"settings",
		page{Title: "Settings", Nav: "settings", Authed: true, Flash: r.URL.Query().Get("flash"), Data: s},
		http.StatusOK,
	)
}

func (h *Handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	interval, err := strconv.Atoi(strings.TrimSpace(r.FormValue("update_interval_hours")))
	if err != nil {
		h.fail(w, r, panel.InvalidSubscriptionUpdateInterval())

		return
	}

	rules, err := panel.ParseUserAgentRules(r.FormValue("user_agent_rules"))
	if err != nil {
		h.fail(w, r, err)

		return
	}

	in := panel.SubscriptionSettings{
		Title:               r.FormValue("title"),
		UpdateIntervalHours: interval,
		SupportURL:          r.FormValue("support_url"),
		ProfilePageURL:      r.FormValue("profile_page_url"),
		Announce:            r.FormValue("announce"),
		SingBoxDisabled:     r.FormValue("singbox_enabled") == "",
		ClashDisabled:       r.FormValue("clash_enabled") == "",
		UserAgentRules:      rules,
		ClashRoutingEnabled: r.FormValue("clash_routing_enabled") != "",
		ClashRoutingYAML:    r.FormValue("clash_routing_yaml"),
	}
	if err := h.Settings.UpdateSubscriptionSettings(r.Context(), in); err != nil {
		h.fail(w, r, err)

		return
	}

	flash := "Settings saved. Clients get them on the next subscription update"
	h.redirect(w, r, "/settings?flash="+url.QueryEscape(flash))
}
