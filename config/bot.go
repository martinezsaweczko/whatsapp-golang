package config

import (
	"fmt"
	"strings"
)

// BotConfig holds all WhatsApp bot specific configuration
type BotConfig struct {
	// WhatsApp identity
	BotNumber          string   // Bot phone number (e.g. 34936674253)
	MentionedBotNumber string   // Bot JID in @lid form used in group mentions
	ToNotification     string   // JID that receives restart notifications
	TrollNumbers       []string // JIDs that get the "troll" AI personality

	// Kiosk folders and retentions (in hours)
	NacionalFolder         string
	RetentionNacional      int
	InternacionalFolder    string
	RetentionInternacional int
	MagazineFolder         string
	RetentionMagazine      int

	// URLServer is the public base URL (host:port) used when building download links
	URLServer string

	// Storage
	DBPath        string // Application database (subscriptions, jwt, usage)
	SessionDBPath string // whatsmeow session store

	// External APIs
	OpenAIAPIKey     string
	OpenAIModel      string
	WeatherAPIKey    string
	WeatherAPIURL    string
	WeatherNoteWords int
	StabilityAPIKey  string
	EsiosAPIKey      string

	// CommandTimeout is the max execution time (seconds) for a WhatsApp command
	CommandTimeout int

	// PairPhone, when set and no session exists, requests a pairing code for
	// this phone number instead of showing a QR code
	PairPhone string

	// ElectricityCacheDir is where the kwh-*.json/png cache files are stored
	ElectricityCacheDir string
}

func (b *BotConfig) validate() error {
	var errs []string

	if b.BotNumber == "" {
		errs = append(errs, "bot number cannot be empty")
	}
	if b.ToNotification == "" {
		errs = append(errs, "notification JID cannot be empty")
	}

	for _, folder := range []struct{ name, value string }{
		{"nacional", b.NacionalFolder},
		{"internacional", b.InternacionalFolder},
		{"magazine", b.MagazineFolder},
	} {
		if folder.value == "" {
			errs = append(errs, fmt.Sprintf("%s folder cannot be empty", folder.name))
		}
	}

	for _, ret := range []struct {
		name  string
		value int
	}{
		{"nacional", b.RetentionNacional},
		{"internacional", b.RetentionInternacional},
		{"magazine", b.RetentionMagazine},
	} {
		if ret.value <= 0 {
			errs = append(errs, fmt.Sprintf("%s retention must be positive (hours)", ret.name))
		}
	}

	if b.URLServer == "" {
		errs = append(errs, "url server cannot be empty")
	}
	if b.DBPath == "" {
		errs = append(errs, "db path cannot be empty")
	}
	if b.SessionDBPath == "" {
		errs = append(errs, "session db path cannot be empty")
	}
	if b.CommandTimeout <= 0 {
		errs = append(errs, "command timeout must be positive")
	}

	if len(errs) > 0 {
		return fmt.Errorf("bot configuration invalid: %s", strings.Join(errs, "; "))
	}
	return nil
}
