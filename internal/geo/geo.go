// Package geo enriches client IPs with country and network data from local
// MaxMind GeoLite2 databases. The databases are optional and user-supplied
// (MaxMind's license requires an account signup, so they are never bundled);
// with no databases loaded every lookup misses and the rest of the analytics
// pipeline simply records nothing. Lookups are pure in-memory reads —
// microseconds, safe to call from the analytics writer.
package geo

import (
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Info is one enrichment result. CC is the ISO 3166-1 alpha-2 country code.
// GeoLite2 has no ISP database, so ASOrg (the autonomous-system organization)
// is the closest "network" label available.
type Info struct {
	CC      string
	Country string
	ASN     uint32
	ASOrg   string
}

// Status describes what is currently loaded, for the Settings badge.
type Status struct {
	CityLoaded bool   `json:"cityLoaded"`
	ASNLoaded  bool   `json:"asnLoaded"`
	CityError  string `json:"cityError,omitempty"`
	ASNError   string `json:"asnError,omitempty"`
}

// Resolver holds the open databases behind an RWMutex so a settings change
// can swap them under live readers.
type Resolver struct {
	logger *slog.Logger

	mu     sync.RWMutex
	city   *maxminddb.Reader
	asn    *maxminddb.Reader
	status Status
}

func NewResolver(logger *slog.Logger) *Resolver {
	return &Resolver{logger: logger}
}

// Load (re)opens the configured databases. Empty paths unload; a path that
// fails to open records the error in Status but never fails the caller — geo
// is enrichment, not a dependency.
func (r *Resolver) Load(cityPath, asnPath string) {
	open := func(path, want string) (*maxminddb.Reader, string) {
		if path == "" {
			return nil, ""
		}
		db, err := maxminddb.Open(path)
		if err != nil {
			return nil, err.Error()
		}
		// Substring match admits the whole family sharing a record layout
		// (GeoLite2-City / GeoIP2-City / GeoLite2-ASN …). Warn-but-serve: a
		// wrong database still opens, it just decodes to empty records — the
		// warning is what explains the silence.
		if want != "" && !strings.Contains(db.Metadata.DatabaseType, want) {
			r.logger.Warn("geo: database type mismatch — lookups will come up empty",
				"path", path, "type", db.Metadata.DatabaseType, "want", want)
		}
		return db, ""
	}
	city, cityErr := open(cityPath, "City")
	asn, asnErr := open(asnPath, "ASN")

	r.mu.Lock()
	old := []*maxminddb.Reader{r.city, r.asn}
	r.city, r.asn = city, asn
	r.status = Status{
		CityLoaded: city != nil, CityError: cityErr,
		ASNLoaded: asn != nil, ASNError: asnErr,
	}
	r.mu.Unlock()
	for _, db := range old {
		if db != nil {
			db.Close()
		}
	}
	if cityErr != "" {
		r.logger.Warn("geo: city database failed to open", "path", cityPath, "err", cityErr)
	}
	if asnErr != "" {
		r.logger.Warn("geo: asn database failed to open", "path", asnPath, "err", asnErr)
	}
}

// Close releases the databases.
func (r *Resolver) Close() {
	r.Load("", "")
}

// Status reports what is loaded, for the Settings badge.
func (r *Resolver) Status() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// Lookup enriches one address. ok is false for private/unspecified addresses
// and when nothing is loaded or found — callers store nothing in that case.
func (r *Resolver) Lookup(addr netip.Addr) (Info, bool) {
	if !addr.IsValid() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return Info{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var info Info
	found := false
	if r.city != nil {
		var rec struct {
			Country struct {
				ISOCode string            `maxminddb:"iso_code"`
				Names   map[string]string `maxminddb:"names"`
			} `maxminddb:"country"`
		}
		if res := r.city.Lookup(addr); res.Found() && res.Decode(&rec) == nil && rec.Country.ISOCode != "" {
			info.CC = rec.Country.ISOCode
			info.Country = rec.Country.Names["en"]
			found = true
		}
	}
	if r.asn != nil {
		var rec struct {
			ASN   uint32 `maxminddb:"autonomous_system_number"`
			ASOrg string `maxminddb:"autonomous_system_organization"`
		}
		if res := r.asn.Lookup(addr); res.Found() && res.Decode(&rec) == nil && rec.ASN != 0 {
			info.ASN = rec.ASN
			info.ASOrg = rec.ASOrg
			found = true
		}
	}
	return info, found
}

// LookupIP is Lookup for a string address (the analytics store's native key).
func (r *Resolver) LookupIP(ip string) (Info, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return Info{}, false
	}
	return r.Lookup(addr)
}

// String renders a compact status line for logs.
func (s Status) String() string {
	return fmt.Sprintf("city=%v asn=%v", s.CityLoaded, s.ASNLoaded)
}
