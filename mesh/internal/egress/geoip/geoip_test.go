package geoip

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func blobServer(t *testing.T, blob []byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(blob)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestMissingDatabaseLooksUpNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "country.mmdb")
	service := New(Options{Path: path, Refresh: 24 * time.Hour})
	defer service.Close()

	if _, ok := service.Lookup("1.1.1.1"); ok {
		t.Fatal("lookup must fail with no database")
	}
	status := service.Status()
	if status.Loaded || !status.Enabled {
		t.Fatalf("status = %+v", status)
	}
	if !service.Due() {
		t.Fatal("a missing database is always due for update")
	}
}

func TestUpdateRejectsUnusablePayloadsAndKeepsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "country.mmdb")

	// Too small to be a database.
	small := blobServer(t, []byte("not a database"))
	service := New(Options{Path: path, URL: small.URL})
	if err := service.Update(context.Background()); err == nil {
		t.Fatal("tiny payload must be rejected")
	} else if !strings.Contains(err.Error(), "too small") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("rejected download must not create the database file")
	}
	if status := service.Status(); status.LastError == "" {
		t.Fatal("the failure must be reported in status")
	}

	// Large but not a MaxMind database: the format check must stop the swap.
	junk := bytes.Repeat([]byte("x"), 2<<20)
	big := blobServer(t, junk)
	invalid := New(Options{Path: path, URL: big.URL})
	if err := invalid.Update(context.Background()); err == nil {
		t.Fatal("non-database payload must be rejected")
	}
	_ = invalid.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid database must not replace the stored file")
	}

	// A checksum that does not match is refused before any parsing.
	digest := sha256.Sum256(junk)
	wrong := blobServer(t, junk)
	checked := New(Options{Path: path, URL: wrong.URL, SHA256: strings.Repeat("0", 64)})
	if err := checked.Update(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("checksum mismatch error = %v", err)
	}
	if checked.Status().SHA256 == hex.EncodeToString(digest[:]) {
		t.Fatal("a rejected download must not record its digest")
	}
	_ = service.Close()
	_ = checked.Close()
}

func TestUpdateReportsHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	service := New(Options{Path: filepath.Join(t.TempDir(), "country.mmdb"), URL: server.URL})
	defer service.Close()

	if err := service.Update(context.Background()); err == nil {
		t.Fatal("HTTP 404 must surface as an error")
	}
	if status := service.Status(); !strings.Contains(status.LastError, "404") {
		t.Fatalf("status error = %q", status.LastError)
	}
}
