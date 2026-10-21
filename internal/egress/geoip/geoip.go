// Package geoip resolves node addresses to countries from a MaxMind
// compatible database. The database is downloaded and verified by the caller;
// this package only reads it and reports freshness.
package geoip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

// DefaultURL is the MetaCubeX meta-rules-dat mirror of a GeoLite2-compatible
// country database. It needs no account, unlike the upstream MaxMind CDN.
const DefaultURL = "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb"

// countryRecord matches the GeoLite2-Country layout used by the mirror.
type countryRecord struct {
	Country struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"country"`
	Continent struct {
		Code string `maxminddb:"code"`
	} `maxminddb:"continent"`
}

// Result is one lookup answer.
type Result struct {
	Country string `json:"country"`
	Name    string `json:"name,omitempty"`
}

// Reader answers lookups from an open database.
type Reader struct {
	db   *maxminddb.Reader
	path string
}

// Open loads the database at path.
func Open(path string) (*Reader, error) {
	db, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("geoip: open %s: %w", path, err)
	}
	return &Reader{db: db, path: path}, nil
}

// Close releases the memory map.
func (r *Reader) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

// Path reports the file backing this reader.
func (r *Reader) Path() string { return r.path }

// Lookup resolves one IP address. It returns a zero Result for addresses the
// database does not describe, including private and non-IP values.
func (r *Reader) Lookup(address string) (Result, bool) {
	if r == nil || r.db == nil {
		return Result{}, false
	}
	ip := net.ParseIP(address)
	if ip == nil {
		return Result{}, false
	}
	var record countryRecord
	if err := r.db.Lookup(ip, &record); err != nil {
		return Result{}, false
	}
	code := record.Country.ISOCode
	if code == "" {
		return Result{}, false
	}
	name := record.Country.Names["zh-CN"]
	if name == "" {
		name = record.Country.Names["en"]
	}
	return Result{Country: code, Name: name}, true
}

// Status describes the local database for the management API.
type Status struct {
	Enabled     bool       `json:"enabled"`
	Path        string     `json:"path,omitempty"`
	URL         string     `json:"url,omitempty"`
	ModTime     *time.Time `json:"mod_time,omitempty"`
	SizeBytes   int64      `json:"size_bytes,omitempty"`
	SHA256      string     `json:"sha256,omitempty"`
	LastUpdate  *time.Time `json:"last_update,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	NextUpdate  *time.Time `json:"next_update,omitempty"`
	Loaded      bool       `json:"loaded"`
	RefreshDays int        `json:"refresh_days"`
}

// Options configures a Service.
type Options struct {
	Path    string
	URL     string
	SHA256  string
	Refresh time.Duration
	Client  *http.Client
	Now     func() time.Time
}

// Service owns the database file, the open reader and the update schedule.
// The reader is swapped atomically so lookups never block a download.
type Service struct {
	options Options
	client  *http.Client
	now     func() time.Time

	reader atomic.Pointer[Reader]

	mu     sync.Mutex
	status Status
}

// New prepares a service and loads an existing database when one is present.
// A missing file is not an error: the first Update installs it.
func New(options Options) *Service {
	if options.URL == "" {
		options.URL = DefaultURL
	}
	if options.Refresh <= 0 {
		options.Refresh = 7 * 24 * time.Hour
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 5 * time.Minute}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	service := &Service{options: options, client: options.Client, now: options.Now}
	service.status = Status{Enabled: true, Path: options.Path, URL: options.URL, RefreshDays: int(options.Refresh / (24 * time.Hour))}
	if reader, err := Open(options.Path); err == nil {
		service.reader.Store(reader)
		service.status.Loaded = true
		if info, statErr := os.Stat(options.Path); statErr == nil {
			modTime := info.ModTime().UTC()
			service.status.ModTime = &modTime
			service.status.SizeBytes = info.Size()
		}
	}
	return service
}

// Close releases the open database.
func (s *Service) Close() error {
	if reader := s.reader.Swap(nil); reader != nil {
		return reader.Close()
	}
	return nil
}

// Lookup resolves one address, or reports false when no database is loaded.
func (s *Service) Lookup(address string) (Result, bool) {
	reader := s.reader.Load()
	if reader == nil {
		return Result{}, false
	}
	return reader.Lookup(address)
}

// Status returns a copy of the database state.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status
	next := s.nextUpdateLocked()
	status.NextUpdate = next
	return status
}

func (s *Service) nextUpdateLocked() *time.Time {
	if s.status.LastUpdate == nil {
		value := s.now().UTC()
		return &value
	}
	next := s.status.LastUpdate.Add(s.options.Refresh)
	if next.Before(s.now()) {
		value := s.now().UTC()
		return &value
	}
	return &next
}

// Due reports whether the database is missing or older than the refresh window.
func (s *Service) Due() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.LastUpdate == nil {
		return true
	}
	return s.now().After(s.status.LastUpdate.Add(s.options.Refresh))
}

// Update downloads the database, verifies it, and swaps it in. A failed
// download leaves the previous database serving lookups.
func (s *Service) Update(ctx context.Context) error {
	if s.options.Path == "" {
		return errors.New("geoip: no database path configured")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.options.URL, nil)
	if err != nil {
		s.recordError(err)
		return err
	}
	request.Header.Set("Accept", "application/octet-stream")
	response, err := s.client.Do(request)
	if err != nil {
		s.recordError(err)
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err := fmt.Errorf("geoip: %s returned HTTP %d", s.options.URL, response.StatusCode)
		s.recordError(err)
		return err
	}

	if err := os.MkdirAll(filepath.Dir(s.options.Path), 0o755); err != nil {
		s.recordError(err)
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.options.Path), ".geoip-*.mmdb")
	if err != nil {
		s.recordError(err)
		return err
	}
	defer os.Remove(temp.Name())

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hash), response.Body)
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		s.recordError(err)
		return err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if s.options.SHA256 != "" && !strings.EqualFold(digest, s.options.SHA256) {
		err := fmt.Errorf("geoip: checksum mismatch (got %s, want %s)", digest, s.options.SHA256)
		s.recordError(err)
		return err
	}
	if written < 1<<20 {
		err := fmt.Errorf("geoip: downloaded file is too small (%d bytes)", written)
		s.recordError(err)
		return err
	}
	// Validate before replacing: a truncated or wrong-format download must not
	// take the working database offline.
	candidate, err := Open(temp.Name())
	if err != nil {
		s.recordError(err)
		return err
	}
	if err := os.Rename(temp.Name(), s.options.Path); err != nil {
		_ = candidate.Close()
		s.recordError(err)
		return err
	}

	s.mu.Lock()
	previous := s.reader.Swap(candidate)
	s.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	now := s.now().UTC()
	s.mu.Lock()
	s.status.Loaded = true
	s.status.LastUpdate = &now
	s.status.SHA256 = digest
	s.status.SizeBytes = written
	modTime := now
	s.status.ModTime = &modTime
	s.status.LastError = ""
	s.mu.Unlock()
	return nil
}

func (s *Service) recordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LastError = err.Error()
}

// Run refreshes the database on the configured interval until ctx ends. The
// database URL and path come from operator configuration only, never from a
// request, so this loop cannot be pointed at an arbitrary host by an API caller.
func (s *Service) Run(ctx context.Context) {
	for {
		if s.Due() {
			// A failed download leaves the previous database serving lookups;
			// the error surfaces through Status.
			_ = s.Update(ctx)
		}
		timer := time.NewTimer(time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			timer.Stop()
		}
	}
}
