package ai

import (
	"testing"
)

func TestMatchIntent_Deals(t *testing.T) {
	tests := []struct {
		message string
		want    string
	}{
		{"show my deals", "search_deals"},
		{"Show My Deals", "search_deals"},
		{"my pipeline", "search_deals"},
		{"show pipeline", "search_deals"},
		{"top deals", "top_deals"},
		{"biggest deals", "top_deals"},
		{"top 5 deals", "top_deals"},
	}
	for _, tt := range tests {
		got := MatchIntent(tt.message)
		if got != tt.want {
			t.Errorf("MatchIntent(%q) = %q, want %q", tt.message, got, tt.want)
		}
	}
}

func TestMatchIntent_Contacts(t *testing.T) {
	tests := []struct {
		message string
		want    string
	}{
		{"show my contacts", "search_contacts"},
		{"list contacts", "search_contacts"},
		{"my contacts", "search_contacts"},
	}
	for _, tt := range tests {
		got := MatchIntent(tt.message)
		if got != tt.want {
			t.Errorf("MatchIntent(%q) = %q, want %q", tt.message, got, tt.want)
		}
	}
}

func TestMatchIntent_Navigation(t *testing.T) {
	tests := []struct {
		message string
		want    string
	}{
		{"go to deals", "nav_deals"},
		{"open contacts", "nav_contacts"},
		{"go to settings", "nav_settings"},
		{"navigate to tasks", "nav_tasks"},
		{"go to analytics", "nav_analytics"},
	}
	for _, tt := range tests {
		got := MatchIntent(tt.message)
		if got != tt.want {
			t.Errorf("MatchIntent(%q) = %q, want %q", tt.message, got, tt.want)
		}
	}
}

func TestMatchIntent_Forms(t *testing.T) {
	tests := []struct {
		message string
		want    string
	}{
		{"create contact John", "create_contact"},
		{"new contact", "create_contact"},
		{"add a deal", "create_deal"},
		{"create a deal", "create_deal"},
	}
	for _, tt := range tests {
		got := MatchIntent(tt.message)
		if got != tt.want {
			t.Errorf("MatchIntent(%q) = %q, want %q", tt.message, got, tt.want)
		}
	}
}

func TestMatchIntent_Analytics(t *testing.T) {
	tests := []struct {
		message string
		want    string
	}{
		{"pipeline health", "analytics_pipeline"},
		{"pipeline summary", "analytics_pipeline"},
		{"revenue summary", "analytics_revenue"},
		{"total revenue", "analytics_revenue"},
	}
	for _, tt := range tests {
		got := MatchIntent(tt.message)
		if got != tt.want {
			t.Errorf("MatchIntent(%q) = %q, want %q", tt.message, got, tt.want)
		}
	}
}

func TestMatchIntent_Help(t *testing.T) {
	got := MatchIntent("help")
	if got != "help" {
		t.Errorf("MatchIntent(\"help\") = %q, want \"help\"", got)
	}
	got = MatchIntent("what can you do")
	if got != "help" {
		t.Errorf("MatchIntent(\"what can you do\") = %q, want \"help\"", got)
	}
}

func TestMatchIntent_NoMatch(t *testing.T) {
	tests := []string{
		"What strategy should I use for closing deals?",
		"Draft a follow-up email for John",
		"Compare Q1 vs Q2 performance",
		"Tell me a joke",
		"",
	}
	for _, msg := range tests {
		got := MatchIntent(msg)
		if got != "" {
			t.Errorf("MatchIntent(%q) = %q, want empty (no match)", msg, got)
		}
	}
}

func TestExtractAfterKeyword(t *testing.T) {
	tests := []struct {
		message  string
		keywords []string
		want     string
	}{
		{"create contact John Doe", []string{"create contact "}, "John Doe"},
		{"new contact Jane", []string{"new contact "}, "Jane"},
		{"create contact", []string{"create contact "}, ""},
		{"Add a DEAL Big Opportunity", []string{"create deal ", "add deal ", "add a deal "}, "Big Opportunity"},
	}
	for _, tt := range tests {
		got := extractAfterKeyword(tt.message, tt.keywords)
		if got != tt.want {
			t.Errorf("extractAfterKeyword(%q) = %q, want %q", tt.message, got, tt.want)
		}
	}
}

func TestIntentHelp_Content(t *testing.T) {
	result := intentHelp()
	if result == nil {
		t.Fatal("intentHelp() returned nil")
	}
	if result.Text == "" {
		t.Error("intentHelp() returned empty text")
	}
	if len(result.Events) != 0 {
		t.Error("intentHelp() should not emit events")
	}
}
