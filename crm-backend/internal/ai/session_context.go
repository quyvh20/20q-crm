package ai

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ============================================================
// Session Context Cache — lightweight memory for AI follow-ups
// ============================================================
//
// When the intent router fetches CRM data (deals, contacts, tasks),
// a compact context snapshot is stored per session. When a follow-up
// question doesn't match any intent and falls through to AI, this
// context is injected into the system prompt so the AI "remembers"
// what was just shown — including record IDs for actions.
//
// This is an in-memory LRU-like cache with TTL. No DB migration needed.

// SessionContext holds the accumulated context for one chat session.
type SessionContext struct {
	Entries   []ContextEntry
	UpdatedAt time.Time
}

// ContextEntry is one piece of context from a previous intent result.
type ContextEntry struct {
	Intent    string // e.g. "search_deals", "search_contacts"
	Summary   string // compact text for the AI (includes IDs, names, key data)
	Timestamp time.Time
}

const (
	maxEntriesPerSession = 10            // keep last N context entries
	sessionTTL           = 30 * time.Minute // expire inactive sessions
	maxSessions          = 500            // cap total sessions in memory
)

// SessionContextCache is a concurrency-safe in-memory context store.
type SessionContextCache struct {
	mu       sync.RWMutex
	sessions map[uuid.UUID]*SessionContext
}

// NewSessionContextCache creates a new cache.
func NewSessionContextCache() *SessionContextCache {
	c := &SessionContextCache{
		sessions: make(map[uuid.UUID]*SessionContext),
	}
	// Background cleanup every 5 minutes
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			c.cleanup()
		}
	}()
	return c
}

// Push adds a context entry for the given session.
func (c *SessionContextCache) Push(sessionID uuid.UUID, intentName, summary string) {
	if sessionID == uuid.Nil || summary == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	ctx, ok := c.sessions[sessionID]
	if !ok {
		ctx = &SessionContext{}
		c.sessions[sessionID] = ctx
	}

	ctx.Entries = append(ctx.Entries, ContextEntry{
		Intent:    intentName,
		Summary:   summary,
		Timestamp: time.Now(),
	})
	ctx.UpdatedAt = time.Now()

	// Trim to max entries
	if len(ctx.Entries) > maxEntriesPerSession {
		ctx.Entries = ctx.Entries[len(ctx.Entries)-maxEntriesPerSession:]
	}
}

// BuildContextPrompt returns a compact context string for AI injection.
// Returns "" if no context exists for this session.
func (c *SessionContextCache) BuildContextPrompt(sessionID uuid.UUID) string {
	if sessionID == uuid.Nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	ctx, ok := c.sessions[sessionID]
	if !ok || len(ctx.Entries) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n--- SESSION CONTEXT (from earlier in this conversation) ---\n")
	b.WriteString("Use this context to answer follow-up questions. IDs in [brackets] are real UUIDs you can use in tool calls.\n\n")

	for i, e := range ctx.Entries {
		age := time.Since(e.Timestamp).Round(time.Second)
		b.WriteString(fmt.Sprintf("%d. [%s ago] %s\n", i+1, age, e.Summary))
	}

	return b.String()
}

// Clear removes all context for a session (e.g. on "New Chat").
func (c *SessionContextCache) Clear(sessionID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessions, sessionID)
}

// cleanup removes expired sessions to prevent memory leaks.
func (c *SessionContextCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for id, ctx := range c.sessions {
		if now.Sub(ctx.UpdatedAt) > sessionTTL {
			delete(c.sessions, id)
		}
	}

	// If still over max, remove oldest
	for len(c.sessions) > maxSessions {
		var oldestID uuid.UUID
		var oldestTime time.Time
		for id, ctx := range c.sessions {
			if oldestTime.IsZero() || ctx.UpdatedAt.Before(oldestTime) {
				oldestID = id
				oldestTime = ctx.UpdatedAt
			}
		}
		delete(c.sessions, oldestID)
	}
}
