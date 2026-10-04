// Package weather implements the "tiempo:" command: fetches OpenWeatherMap data
// and asks the AI to write a friendly weather note.
package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/martinezsaweczko/whatsappBot-golang/whatsapp"
	"go.opentelemetry.io/otel/trace"
)

// Completer produces an AI completion (consumer-defined interface)
type Completer interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// Service provides weather reports
type Service struct {
	client    *http.Client
	apiURL    string
	apiKey    string
	noteWords int
	ai        Completer
	log       *slog.Logger
	tracer    trace.Tracer
}

// New creates the weather service
func New(client *http.Client, apiURL, apiKey string, noteWords int, ai Completer, log *slog.Logger, tp trace.TracerProvider) *Service {
	return &Service{
		client:    client,
		apiURL:    apiURL,
		apiKey:    apiKey,
		noteWords: noteWords,
		ai:        ai,
		log:       log,
		tracer:    tp.Tracer("services/weather"),
	}
}

// fetch retrieves the raw weather JSON for a city
func (s *Service) fetch(ctx context.Context, city string) (json.RawMessage, error) {
	params := url.Values{
		"q":       {city},
		"units":   {"metric"},
		"appid":   {s.apiKey},
		"exclude": {"current,minutely,hourly,alerts,daily,"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build weather request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("weather request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weather API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read weather response: %w", err)
	}

	return body, nil
}

// Report builds the friendly weather note for a city
func (s *Service) Report(ctx context.Context, city string) (string, error) {
	ctx, span := s.tracer.Start(ctx, "weather.Report")
	defer span.End()

	raw, err := s.fetch(ctx, city)
	if err != nil {
		return "", err
	}

	prompt := fmt.Sprintf("Por favor escribe una nota metereologica en %d palabras con iconos de whatsapp de forma amistosa y alegre segun este json de open weather. Indica la ciudad y pais de la ciudad: %s",
		s.noteWords, string(raw))

	note, err := s.ai.Complete(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("failed to generate weather note: %w", err)
	}

	return note, nil
}

// Commands returns the WhatsApp commands of the weather service
func (s *Service) Commands() []whatsapp.Command {
	return []whatsapp.Command{
		{Name: "weather", Pattern: regexp.MustCompile(`^(?i)tiempo:\s*`), Handler: s.weatherHandler()},
	}
}

// weatherPrefix strips the "tiempo:" prefix
var weatherPrefix = regexp.MustCompile(`^(?i)tiempo:`)

func (s *Service) weatherHandler() whatsapp.CommandHandler {
	return func(ctx context.Context, sender whatsapp.Sender, msg whatsapp.IncomingMessage) error {
		city := strings.TrimSpace(weatherPrefix.ReplaceAllString(msg.Body, ""))
		s.log.Info("Weather requested", "user", msg.PushName, "city", city)

		if city == "" {
			return sender.ReplyText(ctx, msg, "Indica la ciudad. Ejemplo: *_tiempo: Madrid_*")
		}

		note, err := s.Report(ctx, city)
		if err != nil {
			s.log.Error("Weather report failed", "city", city, "error", err)
			return sender.ReplyText(ctx, msg, "No he podido obtener el tiempo para "+city)
		}

		return sender.ReplyText(ctx, msg, note)
	}
}
