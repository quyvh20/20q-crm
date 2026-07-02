package http

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// TestRegisterObjectRegistryRoutes guards the gin route tree for the uniform
// record API: registration must not panic (param/static conflicts panic at
// registration, which go build can't catch) and every endpoint must be wired.
func TestRegisterObjectRegistryRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("registry route registration panicked: %v", r)
		}
	}()

	e := gin.New()
	registerObjectRegistryRoutes(e.Group("/api"), NewObjectRegistryHandler(nil, nil, nil), NewRecordHandler(nil, nil, nil, nil, nil, nil), NewPermissionHandler(nil), NewSearchHandler(nil), NewObjectLayoutHandler(nil))

	want := map[string]bool{
		"GET /api/registry/objects":                                    false,
		"GET /api/registry/search":                                     false,
		"GET /api/registry/objects/:slug/schema":                       false,
		"PUT /api/registry/objects/:slug/number-prefix":                false,
		"GET /api/registry/objects/:slug/records":                      false,
		"GET /api/registry/objects/:slug/records/:id":                  false,
		"GET /api/registry/objects/:slug/records/:id/page":             false,
		"GET /api/registry/objects/:slug/records/:id/related-lists":    false,
		"POST /api/registry/objects/:slug/records":                     false,
		"PATCH /api/registry/objects/:slug/records/:id":                false,
		"DELETE /api/registry/objects/:slug/records/:id":               false,
		"GET /api/registry/objects/:slug/records/:id/links":            false,
		"POST /api/registry/objects/:slug/records/:id/links":           false,
		"GET /api/registry/objects/:slug/records/:id/tags":             false,
		"POST /api/registry/objects/:slug/records/:id/tags":            false,
		"DELETE /api/registry/objects/:slug/records/:id/tags/:tagId":   false,
		"DELETE /api/registry/links/:id":                               false,
		"GET /api/registry/objects/:slug/records/:id/audit":            false,
		"GET /api/registry/objects/:slug/field-permissions":            false,
		"PUT /api/registry/objects/:slug/field-permissions":            false,
		"GET /api/registry/permissions":                                false,
		"PUT /api/registry/permissions":                                false,
		// P8 layout routes:
		"GET /api/registry/objects/:slug/layouts":                      false,
		"POST /api/registry/objects/:slug/layouts":                     false,
		"PATCH /api/registry/objects/:slug/layouts/:id":                false,
		"DELETE /api/registry/objects/:slug/layouts/:id":               false,
		"PUT /api/registry/objects/:slug/layouts/:id/roles":            false,
	}
	for _, r := range e.Routes() {
		key := r.Method + " " + r.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("expected route not registered: %s", key)
		}
	}
}
