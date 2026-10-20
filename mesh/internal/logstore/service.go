package logstore

import (
	"context"
	"log/slog"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/telemetry"
)

// Options configures a Service.
type Options struct {
	Path          string
	RetentionDays int
	MaxRows       int
	BufferSize    int
	Logger        *slog.Logger
}

// Service owns the database and the buffered writer behind it. The gateway hands
// it records through the telemetry sink interface; the management API reads
// through the query methods.
type Service struct {
	store  *Store
	writer *Writer
}

// Open creates the database and its writer. An empty path disables persistence
// and returns a nil service, which every method tolerates.
func Open(options Options) (*Service, error) {
	if options.Path == "" {
		return nil, nil
	}
	store, err := OpenStore(options.Path)
	if err != nil {
		return nil, err
	}
	service := &Service{store: store, writer: NewWriter(store, options.BufferSize, options.Logger)}
	service.writer.SetRetention(options.RetentionDays, options.MaxRows)
	return service, nil
}

// Start begins the flush and retention loops.
func (s *Service) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.writer.runLoop(ctx)
}

// SetRetention updates the age window and row cap without reopening the store.
func (s *Service) SetRetention(retentionDays, maxRows int) {
	if s == nil {
		return
	}
	s.writer.SetRetention(retentionDays, maxRows)
}

// Close flushes queued records and releases the database.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	if err := s.writer.Close(); err != nil {
		_ = s.store.Close()
		return err
	}
	return s.store.Close()
}

// Path returns the database file.
func (s *Service) Path() string {
	if s == nil {
		return ""
	}
	return s.store.Path()
}

// PersistRequest implements telemetry.RequestSink.
func (s *Service) PersistRequest(record telemetry.UpstreamRequest) {
	if s == nil {
		return
	}
	s.writer.EnqueueRequest(Request{
		RequestID: record.RequestID, At: record.Time, Session: record.Session, ServerKeyID: record.ServerKeyID,
		Model: record.Model, Tier: record.Tier, KeyID: record.KeyID, Channel: record.Channel,
		Anonymous: record.Anonymous, Proxy: record.Proxy, ExitNode: record.ExitNode, ExitID: record.ExitID,
		Attempts: record.Attempts, Status: record.Status, DurationMS: record.DurationMS,
		Success: record.Success, Outcome: record.Outcome,
	})
}

// PersistAttempt implements telemetry.RequestSink.
func (s *Service) PersistAttempt(record telemetry.UpstreamAttempt) {
	if s == nil {
		return
	}
	s.writer.EnqueueAttempt(Attempt{
		RequestID: record.RequestID, At: record.Time, Attempt: record.Attempt, Model: record.Model,
		Tier: record.Tier, KeyID: record.KeyID, Channel: record.Channel, Anonymous: record.Anonymous,
		Proxy: record.Proxy, ExitNode: record.ExitNode, ExitID: record.ExitID, Status: record.Status,
		DurationMS: record.DurationMS, Success: record.Success, Outcome: record.Outcome,
	})
}

// Requests returns one filtered page, newest first.
func (s *Service) Requests(ctx context.Context, query Query) (RequestPage, error) {
	if s == nil {
		return RequestPage{Requests: []Request{}}, nil
	}
	return s.store.Requests(ctx, query)
}

// Detail returns the stored request and its attempt chain.
func (s *Service) Detail(ctx context.Context, requestID string) (Request, []Attempt, bool, error) {
	if s == nil {
		return Request{}, nil, false, nil
	}
	record, found, err := s.store.RequestByExternalID(ctx, requestID)
	if err != nil || !found {
		return record, nil, found, err
	}
	attempts, err := s.store.AttemptsOf(ctx, requestID)
	return record, attempts, true, err
}

// Facets lists the filter values present in the log.
func (s *Service) Facets(ctx context.Context) (map[string][]string, error) {
	if s == nil {
		return map[string][]string{}, nil
	}
	return s.store.Facets(ctx)
}

// Stats reports the size, freshness and dropped count.
func (s *Service) Stats(ctx context.Context) Stats {
	if s == nil {
		return Stats{}
	}
	stats, err := s.writer.Stats(ctx)
	if err != nil {
		stats = Stats{Enabled: true, Path: s.store.Path()}
	}
	stats.RetentionDays, stats.MaxRows = s.writer.Retention()
	return stats
}

// Purge empties the stored log.
func (s *Service) Purge(ctx context.Context) (int64, error) {
	if s == nil {
		return 0, nil
	}
	return s.store.Purge(ctx)
}
