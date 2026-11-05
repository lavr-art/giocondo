package web

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rsc.io/qr"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
)

type usersData struct {
	Users []panel.UserView
}

func (h *Handler) usersPage(w http.ResponseWriter, r *http.Request) {
	users, err := h.Users.ListUsers(r.Context())
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.render(
		w, r,
		pageUsers,
		page{Title: "Users", Nav: pageUsers, Authed: true, Flash: r.URL.Query().Get("flash"), Data: usersData{Users: users}},
		http.StatusOK,
	)
}

// parseGB converts "100" or "1.5" GB into bytes; empty means 0 (no limit).
func parseGB(s string) (int64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" {
		return 0, nil
	}

	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || v > 1e6 || math.IsNaN(v) {
		return 0, fmt.Errorf("%w: the GB limit must be a number from 0", panel.ErrInvalid)
	}

	return int64(v * (1 << 30)), nil
}

func parseDays(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	d, err := strconv.Atoi(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%w: days must be an integer from 0", panel.ErrInvalid)
	}

	return d, nil
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	limit, err := parseGB(r.FormValue("limit_gb"))
	if err != nil {
		h.fail(w, r, err)

		return
	}

	days, err := parseDays(r.FormValue("days"))
	if err != nil {
		h.fail(w, r, err)

		return
	}

	u, err := h.Users.CreateUser(
		r.Context(),
		panel.CreateUserInput{Name: r.FormValue("name"), TrafficLimitBytes: limit, Days: days},
	)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.redirect(w, r, fmt.Sprintf("/users/%d", u.ID))
}

type userData struct {
	User    panel.UserView
	WGNodes []panel.NodeView
}

func (h *Handler) userPage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	u, err := h.Users.GetUser(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	nodes, err := h.Queries.ListNodes(r.Context())
	if err != nil {
		h.fail(w, r, err)

		return
	}

	d := userData{User: u}
	if u.HasWireGuard {
		for _, n := range nodes {
			for _, in := range n.Inbounds {
				if in.Type == protocol.TypeWireGuard && n.Status != string(panel.NodeDisabled) {
					d.WGNodes = append(d.WGNodes, n)

					break
				}
			}
		}
	}

	h.render(
		w, r,
		"user",
		page{Title: u.Name, Nav: pageUsers, Authed: true, Flash: r.URL.Query().Get("flash"), Data: d},
		http.StatusOK,
	)
}

func (h *Handler) userQR(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	u, err := h.Users.GetUser(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	code, err := qr.Encode(u.SubscriptionURL, qr.M)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	code.Scale = 8

	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(code.PNG())
}

// userWG serves the user's wg-quick config for a node (for clients without a subscription).
func (h *Handler) userWG(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	nodeID, err := strconv.ParseInt(r.PathValue("node"), 10, 64)
	if err != nil {
		h.fail(w, r, fmt.Errorf("%w: bad node id", panel.ErrInvalid))

		return
	}

	conf, name, err := h.Queries.ExportWireGuard(r.Context(), id, nodeID)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	_, _ = w.Write(conf)
}

// userAction handles actions from the table and the user page. An htmx request gets
// the updated table row, otherwise it redirects back to the user page.
func (h *Handler) userAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	ctx := r.Context()

	var (
		u     panel.UserView
		flash string
	)

	switch r.PathValue("action") {
	case "extend":
		days, perr := parseDays(r.FormValue("days"))
		if perr != nil {
			h.fail(w, r, perr)

			return
		}

		u, err = h.Users.Extend(ctx, id, days)
		if err == nil && r.FormValue("reset_traffic") == "on" {
			u, err = h.Users.ResetTraffic(ctx, id)
		}

		flash = "Term extended"
	case "limit":
		limit, perr := parseGB(r.FormValue("limit_gb"))
		if perr != nil {
			h.fail(w, r, perr)

			return
		}

		u, err = h.Users.UpdateUser(ctx, id, panel.UpdateUserInput{TrafficLimitBytes: &limit})
		flash = "Limit changed"
	case "expiry":
		in := panel.UpdateUserInput{ClearExpiry: strings.TrimSpace(r.FormValue("expires")) == ""}
		if !in.ClearExpiry {
			t, perr := time.Parse("2006-01-02", r.FormValue("expires"))
			if perr != nil {
				h.fail(w, r, fmt.Errorf("%w: date must be YYYY-MM-DD", panel.ErrInvalid))

				return
			}

			in.ExpiresAt = &t
		}

		u, err = h.Users.UpdateUser(ctx, id, in)
		flash = "Term changed"
	case actionEnable, actionDisable:
		on := r.PathValue("action") == actionEnable
		u, err = h.Users.UpdateUser(ctx, id, panel.UpdateUserInput{Enabled: &on})
		flash = map[bool]string{true: "User enabled", false: "User disabled"}[on]
	case "reset-traffic":
		u, err = h.Users.ResetTraffic(ctx, id)
		flash = "Traffic reset"
	case "rotate-token":
		u, err = h.Users.RotateToken(ctx, id)
		flash = "New link issued, the old one no longer works"
	case "delete":
		if err := h.Users.DeleteUser(ctx, id); err != nil {
			h.fail(w, r, err)

			return
		}

		if isHTMX(r) {
			w.WriteHeader(http.StatusOK) // empty response: htmx removes the row (hx-swap=delete)

			return
		}

		h.redirect(w, r, "/users?flash="+url.QueryEscape("User deleted"))

		return
	default:
		http.NotFound(w, r)

		return
	}

	if err != nil {
		h.fail(w, r, err)

		return
	}

	if isHTMX(r) && r.FormValue("from") == "list" {
		h.renderPartial(w, r, pageUsers, "user_row", u)

		return
	}

	h.redirect(w, r, fmt.Sprintf("/users/%d?flash=%s", id, url.QueryEscape(flash)))
}
