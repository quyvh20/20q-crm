package automation

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// schemaCacheEntry holds a cached schema response with its expiry time.
type schemaCacheEntry struct {
	data      *SchemaResponse
	expiresAt time.Time
}

// SchemaCache is a thread-safe, per-org, in-memory cache for workflow schema
// responses. Entries expire after a configurable TTL (default 60s) and can
// be explicitly invalidated when stages, tags, fields, or custom objects change.
type SchemaCache struct {
	mu      sync.RWMutex
	entries map[uuid.UUID]*schemaCacheEntry
	ttl     time.Duration
}

// NewSchemaCache creates a new cache with the given TTL.
func NewSchemaCache(ttl time.Duration) *SchemaCache {
	return &SchemaCache{
		entries: make(map[uuid.UUID]*schemaCacheEntry),
		ttl:     ttl,
	}
}

// Get returns the cached schema for an org if it exists and hasn't expired.
func (c *SchemaCache) Get(orgID uuid.UUID) *SchemaResponse {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[orgID]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil
	}
	return entry.data
}

// Set stores a schema response for an org with the configured TTL.
func (c *SchemaCache) Set(orgID uuid.UUID, data *SchemaResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[orgID] = &schemaCacheEntry{
		data:      data,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// Invalidate removes the cached schema for a specific org.
// Call this when stages, tags, custom fields, custom objects, or members change.
func (c *SchemaCache) Invalidate(orgID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, orgID)
}

// InvalidateAll clears the entire cache (e.g., on deploy or global config change).
func (c *SchemaCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[uuid.UUID]*schemaCacheEntry)
}

// Len returns the number of cached entries (for testing/monitoring).
func (c *SchemaCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
