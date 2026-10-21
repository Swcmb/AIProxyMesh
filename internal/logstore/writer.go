package logstore

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const (
	writerDefaultBuffer  = 8192
	writerFlushSize      = 100
	writerFlushInterval  = 500 * time.Millisecond
	writerPruneInterval  = 10 * time.Minute
	writerStopDrainLimit = 4096
)

// entry is one queued record. Requests and attempts share a queue so a burst of
// retries cannot fill the buffer with attempts alone.
type entry struct {
	isRequest bool
	request   Request
	attempt   Attempt
}

// Writer buffers log records off the request path and persists them in batches.
// Enqueue never blocks: when the buffer is full the record is dropped and
// counted, because losing history must not slow down inference.
type Writer struct {
	store   *Store
	logger  *slog.Logger
	queue   chan entry
	dropped atomic.Int64
	written atomic.Int64
	// Retention is changed by configuration at runtime, so the prune loop reads
	// it through atomics instead of a captured parameter.
	retention atomic.Int64
	maxRows   atomic.Int64

	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup
}

// NewWriter builds a writer for the store. A buffer size below one record falls
// back to the default.
func NewWriter(store *Store, bufferSize int, logger *slog.Logger) *Writer {
	if bufferSize <= 0 {
		bufferSize = writerDefaultBuffer
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Writer{store: store, logger: logger, queue: make(chan entry, bufferSize), done: make(chan struct{})}
}

// EnqueueRequest records one completed request without waiting for the write.
func (w *Writer) EnqueueRequest(record Request) {
	w.enqueue(entry{isRequest: true, request: record})
}

// EnqueueAttempt records one upstream attempt.
func (w *Writer) EnqueueAttempt(record Attempt) {
	w.enqueue(entry{attempt: record})
}

func (w *Writer) enqueue(item entry) {
	select {
	case w.queue <- item:
	default:
		// The buffer is full: drop the newest record and surface the count in
		// the stats endpoint rather than stalling the request path.
		w.dropped.Add(1)
	}
}

// runLoop starts the flush and retention goroutine. Retention must be set first
// with SetRetention.
func (w *Writer) runLoop(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.run(ctx)
	}()
}

// SetRetention updates the age and row caps the prune sweep applies.
func (w *Writer) SetRetention(retentionDays int, maxRows int) {
	w.retention.Store(int64(retentionDays) * int64(24*time.Hour))
	w.maxRows.Store(int64(maxRows))
}

// Retention reports the configured age window in days and the row cap.
func (w *Writer) Retention() (int, int) {
	return int(w.retention.Load() / int64(24*time.Hour)), int(w.maxRows.Load())
}

func (w *Writer) run(ctx context.Context) {
	flushTicker := time.NewTicker(writerFlushInterval)
	defer flushTicker.Stop()
	pruneTicker := time.NewTicker(writerPruneInterval)
	defer pruneTicker.Stop()

	requests := make([]Request, 0, writerFlushSize)
	attempts := make([]Attempt, 0, writerFlushSize)

	for {
		select {
		case <-ctx.Done():
			w.drain(&requests, &attempts)
			return
		case <-w.done:
			w.drain(&requests, &attempts)
			return
		case item := <-w.queue:
			if item.isRequest {
				requests = append(requests, item.request)
			} else {
				attempts = append(attempts, item.attempt)
			}
			if len(requests)+len(attempts) >= writerFlushSize {
				requests, attempts = w.flush(ctx, requests, attempts)
			}
		case <-flushTicker.C:
			requests, attempts = w.flush(ctx, requests, attempts)
		case <-pruneTicker.C:
			w.pruneNow(ctx)
		}
	}
}

// drain performs a bounded final flush so a shutdown does not lose more than the
// records still queued at that moment.
func (w *Writer) drain(requests *[]Request, attempts *[]Attempt) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for processed := 0; processed < writerStopDrainLimit; processed++ {
		select {
		case item := <-w.queue:
			if item.isRequest {
				*requests = append(*requests, item.request)
			} else {
				*attempts = append(*attempts, item.attempt)
			}
			if len(*requests)+len(*attempts) >= writerFlushSize {
				*requests, *attempts = w.flush(ctx, *requests, *attempts)
			}
		default:
			w.flush(ctx, *requests, *attempts)
			w.pruneNow(ctx)
			return
		}
	}
	w.flush(ctx, *requests, *attempts)
}

func (w *Writer) flush(ctx context.Context, requests []Request, attempts []Attempt) ([]Request, []Attempt) {
	if len(requests) == 0 && len(attempts) == 0 {
		return requests[:0], attempts[:0]
	}
	if err := w.store.insertBatch(ctx, requests, attempts); err != nil {
		w.logger.Error("request log write failed", "component", "logstore", "event", "request_log_write_failed",
			"requests", len(requests), "attempts", len(attempts), "error", err)
		w.dropped.Add(int64(len(requests) + len(attempts)))
	} else {
		w.written.Add(int64(len(requests) + len(attempts)))
	}
	return requests[:0], attempts[:0]
}

// pruneNow applies the currently configured retention window and row cap.
func (w *Writer) pruneNow(ctx context.Context) {
	olderThan := time.Time{}
	if nanos := w.retention.Load(); nanos > 0 {
		olderThan = time.Now().Add(-time.Duration(nanos))
	}
	removed, err := w.store.Prune(ctx, olderThan, int(w.maxRows.Load()))
	if err != nil {
		w.logger.Error("request log retention failed", "component", "logstore", "event", "request_log_prune_failed", "error", err)
		return
	}
	if removed > 0 {
		w.logger.Info("request log retention", "component", "logstore", "event", "request_log_pruned", "removed", removed)
	}
}

// Close stops the writer and flushes what is still queued.
func (w *Writer) Close() error {
	w.closeOnce.Do(func() { close(w.done) })
	w.wg.Wait()
	return nil
}

// Stats returns the store counters plus the in-memory drop count.
func (w *Writer) Stats(ctx context.Context) (Stats, error) {
	stats, err := w.store.Stats(ctx)
	if err != nil {
		return stats, err
	}
	stats.Dropped = w.dropped.Load()
	return stats, nil
}

// Written reports how many records this process has persisted.
func (w *Writer) Written() int64 { return w.written.Load() }
