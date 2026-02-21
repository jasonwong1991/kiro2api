package auth

import (
	"kiro2api/types"
	"testing"
	"time"
)

func TestParseResetTimestamp(t *testing.T) {
	secTs := float64(1767225600) // 2026-01-01T00:00:00Z
	msTs := secTs * 1000

	secTime, ok := parseResetTimestamp(secTs)
	if !ok {
		t.Fatalf("expected seconds timestamp to be parsed")
	}
	if secTime.UTC().Format(time.RFC3339) != "2026-01-01T00:00:00Z" {
		t.Fatalf("unexpected seconds parse result: %s", secTime.UTC().Format(time.RFC3339))
	}

	msTime, ok := parseResetTimestamp(msTs)
	if !ok {
		t.Fatalf("expected milliseconds timestamp to be parsed")
	}
	if msTime.UTC().Format(time.RFC3339) != "2026-01-01T00:00:00Z" {
		t.Fatalf("unexpected milliseconds parse result: %s", msTime.UTC().Format(time.RFC3339))
	}
}

func TestGetNextResetTimePrefersAPITimestamp(t *testing.T) {
	want := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	usage := &types.UsageLimits{
		NextDateReset: float64(want.Unix()),
	}

	got := GetNextResetTime(usage)
	if !got.Equal(want) {
		t.Fatalf("expected %s, got %s", want.Format(time.RFC3339), got.UTC().Format(time.RFC3339))
	}
}

func TestGetNextResetTimeFallbackUsesConfiguredTimezone(t *testing.T) {
	t.Setenv("KIRO_BILLING_TIMEZONE", "UTC")

	now := time.Now().UTC()
	year := now.Year()
	month := now.Month() + 1
	if month > 12 {
		month = 1
		year++
	}
	want := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)

	got := GetNextResetTime(nil)
	if !got.Equal(want) {
		t.Fatalf("expected fallback reset %s, got %s", want.Format(time.RFC3339), got.UTC().Format(time.RFC3339))
	}
}

func TestShouldSkipRefreshDueToQuota(t *testing.T) {
	now := time.Now()
	cached := &CachedToken{
		Available:     0,
		NextResetTime: now.Add(2 * time.Hour),
	}
	if !shouldSkipRefreshDueToQuota(cached, now) {
		t.Fatalf("expected skip=true when quota exhausted and reset time is in future")
	}

	cached.NextResetTime = now.Add(-2 * time.Hour)
	if shouldSkipRefreshDueToQuota(cached, now) {
		t.Fatalf("expected skip=false when reset time already passed")
	}
}
