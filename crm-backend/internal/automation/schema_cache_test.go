package automation

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaCache_GetSetExpiry(t *testing.T) {
	cache := NewSchemaCache(100 * time.Millisecond)

	orgID := uuid.New()

	// Miss on empty cache
	assert.Nil(t, cache.Get(orgID), "empty cache should return nil")

	// Set and hit
	data := &SchemaResponse{
		Stages: []SchemaStage{{ID: "s1", Name: "Lead", Color: "#000", Order: 0}},
		Tags:   []SchemaTag{{ID: "t1", Name: "VIP", Color: "#F00"}},
	}
	cache.Set(orgID, data)
	cached := cache.Get(orgID)
	require.NotNil(t, cached, "cache should return stored data")
	assert.Equal(t, "Lead", cached.Stages[0].Name)
	assert.Len(t, cached.Tags, 1)

	// Wait for expiry
	time.Sleep(150 * time.Millisecond)
	assert.Nil(t, cache.Get(orgID), "expired entry should return nil")
}

func TestSchemaCache_PerOrgIsolation(t *testing.T) {
	cache := NewSchemaCache(5 * time.Second)

	orgA := uuid.New()
	orgB := uuid.New()

	cache.Set(orgA, &SchemaResponse{
		Stages: []SchemaStage{{ID: "a1", Name: "Org A Stage"}},
	})
	cache.Set(orgB, &SchemaResponse{
		Stages: []SchemaStage{{ID: "b1", Name: "Org B Stage"}},
	})

	a := cache.Get(orgA)
	b := cache.Get(orgB)
	require.NotNil(t, a)
	require.NotNil(t, b)
	assert.Equal(t, "Org A Stage", a.Stages[0].Name)
	assert.Equal(t, "Org B Stage", b.Stages[0].Name)
}

func TestSchemaCache_Invalidate(t *testing.T) {
	cache := NewSchemaCache(5 * time.Second)

	orgA := uuid.New()
	orgB := uuid.New()

	cache.Set(orgA, &SchemaResponse{Stages: []SchemaStage{{Name: "A"}}})
	cache.Set(orgB, &SchemaResponse{Stages: []SchemaStage{{Name: "B"}}})

	// Invalidate orgA only
	cache.Invalidate(orgA)

	assert.Nil(t, cache.Get(orgA), "invalidated org should return nil")
	assert.NotNil(t, cache.Get(orgB), "other org should still be cached")
}

func TestSchemaCache_InvalidateAll(t *testing.T) {
	cache := NewSchemaCache(5 * time.Second)

	for i := 0; i < 10; i++ {
		cache.Set(uuid.New(), &SchemaResponse{})
	}
	assert.Equal(t, 10, cache.Len())

	cache.InvalidateAll()
	assert.Equal(t, 0, cache.Len())
}

func TestSchemaCache_ConcurrentAccess(t *testing.T) {
	cache := NewSchemaCache(1 * time.Second)
	orgID := uuid.New()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			cache.Set(orgID, &SchemaResponse{
				Stages: []SchemaStage{{Name: "stage"}},
			})
		}
		close(done)
	}()

	for i := 0; i < 1000; i++ {
		_ = cache.Get(orgID)
	}
	<-done

	// No race detected = pass (run with -race flag)
	assert.NotNil(t, cache.Get(orgID))
}
