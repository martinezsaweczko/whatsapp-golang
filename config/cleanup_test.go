package config

import (
	"strings"
	"testing"
)

func TestBotConfigValidateKioskCleanup(t *testing.T) {
	enabled := func(c *BotConfig) {
		c.KioskCleanupEnabled = true
		c.KioskCleanupMaxAge = 720
		c.KioskCleanupSchedule = "0 4 * * *"
	}
	tests := []struct {
		name    string
		mutate  func(*BotConfig)
		wantErr string
	}{
		{"enabled with defaults", enabled, ""},
		{"max age equal to the largest retention", func(c *BotConfig) { enabled(c); c.RetentionMagazine = 720 }, ""},
		{"max age below a retention", func(c *BotConfig) { enabled(c); c.KioskCleanupMaxAge = 200 }, "kiosk cleanup max age (200h) must be >= magazine retention (720h)"},
		{"zero max age", func(c *BotConfig) { enabled(c); c.KioskCleanupMaxAge = 0 }, "kiosk cleanup max age must be positive"},
		{"bad cron expression", func(c *BotConfig) { enabled(c); c.KioskCleanupSchedule = "every day" }, "invalid kiosk cleanup schedule"},
		{"empty cron expression", func(c *BotConfig) { enabled(c); c.KioskCleanupSchedule = "" }, "invalid kiosk cleanup schedule"},
		{"disabled skips validation", func(c *BotConfig) {
			c.KioskCleanupEnabled = false
			c.KioskCleanupMaxAge = -1
			c.KioskCleanupSchedule = "nonsense"
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBotConfig()
			tt.mutate(&cfg)
			err := cfg.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
