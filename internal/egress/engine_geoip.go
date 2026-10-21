package egress

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/geoip"
)

// newGeoIPService builds the region resolver described by the configuration.
// It returns nil when the section is absent or switched off.
func newGeoIPService(cfg *config.EgressConfig, logger *slog.Logger) *geoip.Service {
	if !cfg.GeoIP.IsEnabled() {
		return nil
	}
	geo := cfg.GeoIP
	days := geo.RefreshDays
	if days <= 0 {
		days = 7
	}
	return geoip.New(geoip.Options{
		Path:    geo.Path,
		URL:     geo.URL,
		SHA256:  geo.SHA256,
		Refresh: time.Duration(days) * 24 * time.Hour,
	})
}

// reconfigureGeoIP applies a configuration change to the region resolver:
// enabling it starts the service, disabling it releases the database, and a
// changed source or path rebuilds it.
func (e *Engine) reconfigureGeoIP(cfg *config.EgressConfig) {
	if !cfg.GeoIP.IsEnabled() {
		if e.geoip != nil {
			_ = e.geoip.Close()
			e.geoip = nil
			e.geoipConfig = nil
		}
		return
	}
	if e.geoip != nil && e.geoipConfig != nil && *e.geoipConfig == *cfg.GeoIP {
		return
	}
	if e.geoip != nil {
		_ = e.geoip.Close()
	}
	e.geoip = newGeoIPService(cfg, e.logger)
	geo := *cfg.GeoIP
	e.geoipConfig = &geo
	if e.ctx != nil {
		e.StartGeoIP(e.ctx)
	}
}

// StartGeoIP begins the periodic database refresh. It is safe to call once per
// engine start.
func (e *Engine) StartGeoIP(ctx context.Context) {
	service := e.geoipInstance()
	if service == nil {
		return
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		service.Run(ctx)
	}()
}

func (e *Engine) geoipInstance() *geoip.Service {
	settings := e.settings.Load()
	if settings == nil || !settings.egress.GeoIP.IsEnabled() {
		return nil
	}
	return e.geoip
}

// GeoIPEnabled reports whether region resolution is configured.
func (e *Engine) GeoIPEnabled() bool { return e.geoipInstance() != nil }

// GeoIPStatus describes the local country database.
func (e *Engine) GeoIPStatus() (geoip.Status, bool) {
	service := e.geoipInstance()
	if service == nil {
		return geoip.Status{}, false
	}
	return service.Status(), true
}

// GeoIPLookup resolves one IP address to a country.
func (e *Engine) GeoIPLookup(address string) (geoip.Result, bool) {
	service := e.geoipInstance()
	if service == nil {
		return geoip.Result{}, false
	}
	return service.Lookup(address)
}

// UpdateGeoIP downloads and swaps the country database immediately.
func (e *Engine) UpdateGeoIP(ctx context.Context) error {
	service := e.geoipInstance()
	if service == nil {
		return errors.New("egress: geoip is disabled")
	}
	return service.Update(ctx)
}

// regionFor resolves a node address to its country code.
func (e *Engine) regionFor(address string) (geoip.Result, bool) {
	return e.GeoIPLookup(address)
}
