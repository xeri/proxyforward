package geo

import (
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures are MaxMind's published test databases (test-data/ in
// github.com/maxmind/MaxMind-DB). Known contents used below:
//   City: 2.125.160.216 → GB (Boxford)
//   ASN:  1.128.0.0/11  → AS1221 "Telstra Pty Ltd"
func testResolver(t *testing.T, city, asn bool) *Resolver {
	t.Helper()
	r := NewResolver(slog.New(slog.NewTextHandler(io.Discard, nil)))
	cityPath, asnPath := "", ""
	if city {
		cityPath = filepath.Join("testdata", "GeoLite2-City-Test.mmdb")
	}
	if asn {
		asnPath = filepath.Join("testdata", "GeoLite2-ASN-Test.mmdb")
	}
	r.Load(cityPath, asnPath)
	t.Cleanup(r.Close)
	return r
}

func TestLookupCityAndASN(t *testing.T) {
	r := testResolver(t, true, true)
	st := r.Status()
	if !st.CityLoaded || !st.ASNLoaded || st.CityError != "" || st.ASNError != "" {
		t.Fatalf("status = %+v", st)
	}

	info, ok := r.Lookup(netip.MustParseAddr("2.125.160.216"))
	if !ok || info.CC != "GB" || info.Country == "" {
		t.Fatalf("city lookup = %+v ok=%v, want GB", info, ok)
	}

	info, ok = r.Lookup(netip.MustParseAddr("1.128.0.1"))
	if !ok || info.ASN != 1221 || info.ASOrg == "" {
		t.Fatalf("asn lookup = %+v ok=%v, want AS1221", info, ok)
	}

	// An address in neither database misses cleanly.
	if _, ok := r.Lookup(netip.MustParseAddr("203.0.113.99")); ok {
		t.Fatal("unknown address reported found")
	}
}

func TestPrivateAndInvalidAddressesMiss(t *testing.T) {
	r := testResolver(t, true, true)
	for _, ip := range []string{"192.168.1.10", "10.0.0.1", "127.0.0.1", "169.254.9.9", "fe80::1", "::"} {
		if _, ok := r.Lookup(netip.MustParseAddr(ip)); ok {
			t.Fatalf("%s: private/special address reported found", ip)
		}
	}
	if _, ok := r.LookupIP("not-an-ip"); ok {
		t.Fatal("garbage address reported found")
	}
}

func TestNoDatabasesLoaded(t *testing.T) {
	r := testResolver(t, false, false)
	st := r.Status()
	if st.CityLoaded || st.ASNLoaded {
		t.Fatalf("status with nothing loaded = %+v", st)
	}
	if _, ok := r.LookupIP("2.125.160.216"); ok {
		t.Fatal("lookup succeeded with no databases")
	}
}

func TestBadPathReportsErrorButServes(t *testing.T) {
	r := NewResolver(slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.Load(filepath.Join("testdata", "does-not-exist.mmdb"), filepath.Join("testdata", "GeoLite2-ASN-Test.mmdb"))
	t.Cleanup(r.Close)
	st := r.Status()
	if st.CityLoaded || st.CityError == "" {
		t.Fatalf("bad city path status = %+v", st)
	}
	if !st.ASNLoaded {
		t.Fatalf("asn should still load: %+v", st)
	}
	// ASN-only lookups still work.
	if info, ok := r.LookupIP("1.128.0.1"); !ok || info.ASN != 1221 {
		t.Fatalf("asn-only lookup = %+v ok=%v", info, ok)
	}
}

// TestTypeMismatchWarnsButServes: a City database in the ASN slot must still
// load (Status ASNLoaded, no error) but leave a warning explaining why every
// ASN lookup will come up empty.
func TestTypeMismatchWarnsButServes(t *testing.T) {
	var logBuf strings.Builder
	r := NewResolver(slog.New(slog.NewTextHandler(&logBuf, nil)))
	r.Load("", filepath.Join("testdata", "GeoLite2-City-Test.mmdb"))
	t.Cleanup(r.Close)

	st := r.Status()
	if !st.ASNLoaded || st.ASNError != "" {
		t.Fatalf("status = %+v, want loaded with no error", st)
	}
	if !strings.Contains(logBuf.String(), "type mismatch") {
		t.Fatalf("no mismatch warning logged; log = %q", logBuf.String())
	}
	// The right database in the right slot must not warn.
	logBuf.Reset()
	r.Load("", filepath.Join("testdata", "GeoLite2-ASN-Test.mmdb"))
	if strings.Contains(logBuf.String(), "type mismatch") {
		t.Fatalf("false-positive mismatch warning; log = %q", logBuf.String())
	}
}

func TestReloadSwapsUnderReaders(t *testing.T) {
	r := testResolver(t, true, true)
	if _, ok := r.LookupIP("2.125.160.216"); !ok {
		t.Fatal("initial lookup failed")
	}
	r.Load("", "") // unload
	if _, ok := r.LookupIP("2.125.160.216"); ok {
		t.Fatal("lookup succeeded after unload")
	}
	r.Load(filepath.Join("testdata", "GeoLite2-City-Test.mmdb"), "")
	if info, ok := r.LookupIP("2.125.160.216"); !ok || info.CC != "GB" {
		t.Fatalf("lookup after reload = %+v ok=%v", info, ok)
	}
}
