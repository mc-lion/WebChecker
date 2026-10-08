package web

import (
	"testing"

	"webchecker/internal/models"
)

func TestWithDefaultUserAgentPicksFlagged(t *testing.T) {
	uas := []models.UserAgent{
		{ID: 2, Name: "A"},
		{ID: 5, Name: "B", IsDefault: true},
		{ID: 9, Name: "C"},
	}
	mon := withDefaultUserAgent(defaultMonitor(), uas)
	if mon.UserAgentID != 5 {
		t.Fatalf("got %d, want default id 5", mon.UserAgentID)
	}
}

func TestWithDefaultUserAgentKeepsExisting(t *testing.T) {
	mon := defaultMonitor()
	mon.UserAgentID = 9
	got := withDefaultUserAgent(mon, []models.UserAgent{
		{ID: 1, IsDefault: true},
		{ID: 9},
	})
	if got.UserAgentID != 9 {
		t.Fatalf("got %d, want 9", got.UserAgentID)
	}
}

func TestWithDefaultUserAgentFirstIfNoDefault(t *testing.T) {
	mon := withDefaultUserAgent(defaultMonitor(), []models.UserAgent{
		{ID: 3},
		{ID: 4},
	})
	if mon.UserAgentID != 3 {
		t.Fatalf("got %d, want 3", mon.UserAgentID)
	}
}
