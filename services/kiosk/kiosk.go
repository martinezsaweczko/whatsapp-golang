// Package kiosk implements the newspaper kiosk: listing available files
// per category and generating one-time download links.
package kiosk

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/martinezsaweczko/whatsappBot-golang/o11"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Category describes a kiosk category (nacional, internacional, magazine)
type Category struct {
	Name      string        // Short name used as category key ("nacional")
	URLPrefix string        // Path prefix on the file server ("nacional_folder")
	Dir       string        // Filesystem directory with the files
	Retention time.Duration // How long a file stays listed
	// Singular/Plural are used when formatting list messages
	Singular string
	Plural   string
	// CommandWord is the word used to request a file ("periodico", "newspaper", "magazine")
	CommandWord string
	// RequestExample is the command example shown in lists ("periodico:2")
	RequestExample string
}

// FileEntry is a listed file
type FileEntry struct {
	Name    string
	ModTime time.Time
}

// UsageStore records that a user requested a download link (consumer-defined interface)
type UsageStore interface {
	ReportUserUsage(ctx context.Context, user, file string) error
}

// TokenIssuer issues JWT tokens for download links (consumer-defined interface)
type TokenIssuer interface {
	GenerateTokenWithExpiry(subject string, expiry time.Duration) (string, error)
}

// linkTokenTTL is how long the JWT in a direct download link is valid
const linkTokenTTL = 5 * time.Minute

// Service provides kiosk operations
type Service struct {
	categories map[string]Category
	urlServer  string
	store      UsageStore
	jwt        TokenIssuer
	log        *slog.Logger
	tracer     trace.Tracer
	linksTotal metric.Int64Counter
	listsTotal metric.Int64Counter
	purged     metric.Int64Counter
}

// New creates the kiosk service
func New(categories []Category, urlServer string, store UsageStore, jwt TokenIssuer, log *slog.Logger, tp trace.TracerProvider, mp metric.MeterProvider) (*Service, error) {
	linksTotal, err := o11.NewBusinessCounter(mp, "kiosk_links_generated_total", "Download links generated per category")
	if err != nil {
		return nil, fmt.Errorf("failed to create links counter: %w", err)
	}
	listsTotal, err := o11.NewBusinessCounter(mp, "kiosk_lists_served_total", "File lists served per category")
	if err != nil {
		return nil, fmt.Errorf("failed to create lists counter: %w", err)
	}

	purged, err := o11.NewBusinessCounter(mp, "kiosk_files_deleted_total", "Kiosk files deleted by the age-based cleanup per category")
	if err != nil {
		return nil, fmt.Errorf("failed to create deleted files counter: %w", err)
	}

	cats := make(map[string]Category, len(categories))
	for _, c := range categories {
		cats[c.Name] = c
	}

	return &Service{
		categories: cats,
		urlServer:  urlServer,
		store:      store,
		jwt:        jwt,
		log:        log,
		tracer:     tp.Tracer("services/kiosk"),
		linksTotal: linksTotal,
		listsTotal: listsTotal,
		purged:     purged,
	}, nil
}

// List returns the files of a category within the retention window, newest first
func (s *Service) List(ctx context.Context, category string) ([]FileEntry, error) {
	ctx, span := s.tracer.Start(ctx, "kiosk.ListFiles", trace.WithAttributes(attribute.String("category", category)))
	defer span.End()

	cat, ok := s.categories[category]
	if !ok {
		err := fmt.Errorf("unknown kiosk category: %s", category)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	dirEntries, err := os.ReadDir(cat.Dir)
	if err != nil {
		err = fmt.Errorf("failed to read directory %s: %w", cat.Dir, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	cutoff := time.Now().Add(-cat.Retention)
	var files []FileEntry
	for _, e := range dirEntries {
		info, err := e.Info()
		if err != nil {
			s.log.WarnContext(ctx, "Skipping unreadable entry", "dir", cat.Dir, "entry", e.Name(), "error", err)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if info.ModTime().Before(cutoff) {
			continue
		}
		files = append(files, FileEntry{Name: e.Name(), ModTime: info.ModTime()})
	}

	// Newest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})

	return files, nil
}

// PurgeOld deletes the regular files of every category folder whose
// modification time is older than maxAge. It is not recursive: subdirectories
// and their content are left alone, and symlinks are never followed nor
// removed. A folder that cannot be read or a file that cannot be deleted is
// logged and does not stop the rest; the returned error joins every failure,
// next to the number of files actually deleted.
func (s *Service) PurgeOld(ctx context.Context, maxAge time.Duration) (int, error) {
	ctx, span := s.tracer.Start(ctx, "kiosk.PurgeOld", trace.WithAttributes(attribute.String("max_age", maxAge.String())))
	defer span.End()

	if maxAge <= 0 {
		err := fmt.Errorf("purge max age must be positive, got %s", maxAge)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, err
	}

	names := make([]string, 0, len(s.categories))
	for name := range s.categories {
		names = append(names, name)
	}
	sort.Strings(names)

	cutoff := time.Now().Add(-maxAge)
	var errs []error
	total := 0
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		deleted, err := s.purgeDir(ctx, s.categories[name], cutoff)
		total += deleted
		if deleted > 0 {
			s.purged.Add(ctx, int64(deleted), metric.WithAttributes(attribute.String("category", name)))
		}
		if err != nil {
			errs = append(errs, err)
		}
	}

	span.SetAttributes(attribute.Int("deleted", total))
	err := errors.Join(errs...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	s.log.InfoContext(ctx, "Kiosk cleanup finished", "deleted", total, "max_age", maxAge.String(), "errors", len(errs))
	return total, err
}

// purgeDir deletes the regular files of one category folder modified before cutoff.
func (s *Service) purgeDir(ctx context.Context, cat Category, cutoff time.Time) (int, error) {
	entries, err := os.ReadDir(cat.Dir)
	if err != nil {
		err = fmt.Errorf("failed to read directory %s: %w", cat.Dir, err)
		s.log.WarnContext(ctx, "Kiosk cleanup skipped folder", "category", cat.Name, "error", err)
		return 0, err
	}

	var errs []error
	deleted := 0
	for _, e := range entries {
		// DirEntry.Info uses lstat, so a symlink reports its own mode and is skipped
		info, err := e.Info()
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, fmt.Errorf("failed to stat %s: %w", filepath.Join(cat.Dir, e.Name()), err))
			}
			continue
		}
		if !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		full := filepath.Join(cat.Dir, e.Name())
		if err := os.Remove(full); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // removed concurrently, nothing left to do
			}
			err = fmt.Errorf("failed to delete %s: %w", full, err)
			s.log.WarnContext(ctx, "Kiosk cleanup could not delete file", "category", cat.Name, "error", err)
			errs = append(errs, err)
			continue
		}
		deleted++
		s.log.DebugContext(ctx, "Kiosk cleanup deleted file", "category", cat.Name, "file", e.Name(), "mod_time", info.ModTime())
	}
	return deleted, errors.Join(errs...)
}

// ListMessage builds the WhatsApp message listing the files of a category
func (s *Service) ListMessage(ctx context.Context, category string) (string, error) {
	ctx, span := s.tracer.Start(ctx, "kiosk.ListMessage", trace.WithAttributes(attribute.String("category", category)))
	defer span.End()

	cat, ok := s.categories[category]
	if !ok {
		err := fmt.Errorf("unknown kiosk category: %s", category)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	files, err := s.List(ctx, category)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "*Lista de %s.* _Actualizados por orden de llegada de forma descendente_\n", cat.Plural)
	fmt.Fprintf(&b, "Recuerde para solicitar un %s escriba: %s: num\nEjemplo: *_%s_*\n", cat.Singular, cat.CommandWord, cat.RequestExample)

	for i, f := range files {
		fmt.Fprintf(&b, "\n%s num:*%d* %s", cat.Singular, i, f.Name)
	}

	s.listsTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("category", category)))
	return b.String(), nil
}

// Link builds the one-time download link for the file at the given index of a category.
// It reports the user usage and returns the URL plus the file name.
func (s *Service) Link(ctx context.Context, category string, index int, user string) (string, string, error) {
	ctx, span := s.tracer.Start(ctx, "kiosk.BuildLink", trace.WithAttributes(
		attribute.String("category", category),
		attribute.Int("index", index),
	))
	defer span.End()

	cat, ok := s.categories[category]
	if !ok {
		err := fmt.Errorf("unknown kiosk category: %s", category)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", "", err
	}

	files, err := s.List(ctx, category)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", "", err
	}

	if index < 0 || index >= len(files) {
		err := fmt.Errorf("índice %d fuera de rango, hay %d ficheros disponibles en %s", index, len(files), category)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", "", err
	}

	fileName := files[index].Name

	token, err := s.jwt.GenerateTokenWithExpiry("periodico", linkTokenTTL)
	if err != nil {
		err = fmt.Errorf("failed to generate download token: %w", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", "", err
	}

	link := url.URL{
		Scheme:   "https",
		Host:     s.urlServer,
		Path:     "/" + cat.URLPrefix + "/" + fileName,
		RawQuery: "access_token=" + token,
	}

	if err := s.store.ReportUserUsage(ctx, user, fileName); err != nil {
		// Not fatal: the link is still delivered
		span.RecordError(err)
		s.log.Warn("Failed to report user usage", "user", user, "file", fileName, "error", err)
	}

	s.linksTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("category", category)))
	s.log.Info("Generated download link", "user", user, "file", fileName, "category", category)

	return link.String(), fileName, nil
}

// BuildLinkMessage builds the WhatsApp reply containing the download link
func BuildLinkMessage(link, fileName string) string {
	return "Por favor accede al periodico mediante el enlace " + link +
		"\nEl enlace estará disponible sólo una única vez\nPeriodico:" + fileName
}

// ParseIndex extracts the first numeric index found in a command body
// (e.g. "periodico:23" -> 23, "periódico: 5" -> 5)
func ParseIndex(body string) (int, error) {
	rest := strings.TrimLeftFunc(body, func(r rune) bool {
		return r < '0' || r > '9'
	})
	if rest == "" {
		return -1, fmt.Errorf("no se indicó el número de la lista")
	}
	// Cut at the first non-digit (e.g. trailing text)
	end := strings.IndexFunc(rest, func(r rune) bool {
		return r < '0' || r > '9'
	})
	if end >= 0 {
		rest = rest[:end]
	}
	idx, err := strconv.Atoi(rest)
	if err != nil {
		return -1, fmt.Errorf("número de lista inválido: %s", rest)
	}
	return idx, nil
}

// FilePath returns the absolute path of a file inside a category directory.
// It validates the file exists and is regular.
func FilePath(dir, name string) (string, error) {
	// Reject path traversal attempts
	if filepath.Base(name) != name {
		return "", fmt.Errorf("invalid file name")
	}
	full := filepath.Join(dir, name)
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	return full, nil
}
