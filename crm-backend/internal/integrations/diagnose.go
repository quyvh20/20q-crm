package integrations

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Per-connection diagnose (L6.3).
//
// "Reconnect this account" is the only thing the product could say when a Facebook
// connection stopped producing leads, and it is wrong at least as often as it is
// right: the token can be perfectly healthy while the page subscription has lapsed,
// or while every form is still disabled, or while Leads Access has never been
// granted. Each of those needs a different action, and telling an admin to redo OAuth
// for any of them wastes their time and does not fix it.
//
// So this probes the layers a lead actually travels through, in order, and names the
// first one that is broken.
//
// THE SERVER SENDS NO PROSE. Each check reports a key and a status; the frontend
// renders its own sentence from a copy table. That is the same posture the connect
// banner and the retry refusal take, and here it is also a SECURITY control: provider
// errors embed the request URL, and a page token rides in that query string — the L5.2
// token-leak fix exists because exactly that string was reaching a rendered field.
// A fixed vocabulary cannot leak a secret.

// Diagnose check keys, in the order a lead travels.
const (
	checkCredentials  = "credentials"
	checkToken        = "token"
	checkSubscription = "subscription"
	checkForms        = "forms"
)

// Check outcomes.
const (
	checkOK = "ok"
	// checkFail is a definite negative: we asked and the answer was no.
	checkFail = "fail"
	// checkWarn is "working, but nothing will arrive" — a configuration gap rather
	// than a fault.
	checkWarn = "warn"
	// checkUnknown is a result we could not obtain. Deliberately NOT collapsed into
	// `fail`: "we could not ask" and "the answer was no" lead to different actions,
	// and the codebase's liveness rule already refuses that conflation elsewhere.
	checkUnknown = "unknown"
	// checkSkipped marks a layer not probed because an earlier one already failed.
	// Rendering it as OK would be a lie; rendering it as failed would invent a second
	// fault out of the first one.
	checkSkipped = "skipped"
)

type diagnoseCheck struct {
	Key    string `json:"key"`
	Status string `json:"status"`
}

type diagnoseResult struct {
	Checks []diagnoseCheck `json:"checks"`
	// Healthy is true only when every layer passed. A convenience for the UI, not a
	// substitute for the list — the point of this action is WHICH layer.
	Healthy bool `json:"healthy"`
}

// Diagnose probes one connection end to end.
func (h *ConnectionHandler) Diagnose(c *gin.Context) {
	orgID, _, ok := h.actor(c)
	if !ok {
		return
	}
	conn, ok := h.loadConnection(c, orgID)
	if !ok {
		return
	}
	prov, ok := h.svc.registry.Get(conn.Provider)
	if !ok {
		h.err(c, http.StatusBadRequest, "this provider is not available")
		return
	}

	res := diagnoseResult{Checks: make([]diagnoseCheck, 0, 4)}
	add := func(key, status string) { res.Checks = append(res.Checks, diagnoseCheck{Key: key, Status: status}) }

	// 1. Can we read the stored secret at all? A failure here is a key-management
	// problem, not a provider one, and every later probe would fail as a consequence.
	creds, err := h.svc.openCredentials(conn)
	if err != nil {
		add(checkCredentials, checkFail)
		add(checkToken, checkSkipped)
		add(checkSubscription, checkSkipped)
		add(checkForms, checkSkipped)
		c.JSON(http.StatusOK, gin.H{"data": res})
		return
	}
	add(checkCredentials, checkOK)

	// 2. Does the provider still accept it?
	tokenOK := true
	if err := prov.HealthCheck(c.Request.Context(), conn, creds); err != nil {
		if errors.Is(err, ErrProviderCapabilityUnsupported) {
			// A provider with no probe is not a broken provider. Reporting "unknown"
			// keeps every future adapter from reading as failing on the day it ships.
			add(checkToken, checkUnknown)
		} else {
			add(checkToken, checkFail)
			tokenOK = false
		}
	} else {
		add(checkToken, checkOK)
	}

	// 3. Is the provider actually configured to send us anything? This is the layer
	// that was invisible: a lapsed subscription looks exactly like a healthy
	// connection, because nothing arrives and nothing fails.
	if !tokenOK {
		add(checkSubscription, checkSkipped)
	} else {
		switch subscribed, serr := prov.CheckSubscription(c.Request.Context(), conn, creds); {
		case serr != nil && errors.Is(serr, ErrProviderCapabilityUnsupported):
			add(checkSubscription, checkUnknown)
		case serr != nil:
			// Could not ask. A permission error here is also the signal that Leads
			// Access may never have been granted — which is why the copy for this
			// state names both possibilities rather than asserting either.
			add(checkSubscription, checkUnknown)
		case subscribed:
			add(checkSubscription, checkOK)
		default:
			add(checkSubscription, checkFail)
		}
	}

	// 4. Is anything on OUR side ready to receive? A connection can be perfectly
	// healthy and still produce nothing because no form was ever enabled — the
	// commonest "it isn't working" of all, and the only one the admin fixes here.
	n, ferr := h.svc.repo.CountLiveFormSources(c.Request.Context(), orgID, conn.ID)
	switch {
	case ferr != nil:
		add(checkForms, checkUnknown)
	case n == 0:
		add(checkForms, checkWarn)
	default:
		add(checkForms, checkOK)
	}

	res.Healthy = true
	for _, ch := range res.Checks {
		if ch.Status != checkOK {
			res.Healthy = false
			break
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}
