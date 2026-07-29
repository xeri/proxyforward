package app

import "testing"

// TestVersionIsNewer pins the two things that produce a wrong prompt: an
// unreadable version being compared anyway, and a pre-release being ranked
// against the release it precedes the wrong way round.
func TestVersionIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		newer, ok       bool
	}{
		// The ordinary answers.
		{"v1.2.3", "v1.2.2", true, true},
		{"v1.2.3", "v1.2.3", false, true},
		{"v1.2.3", "v1.3.0", false, true},
		{"v2.0.0", "v1.99.99", true, true},
		{"1.2.3", "v1.2.2", true, true}, // the "v" is decoration on either side

		// A short tag is padded, not refused: "v1.2" is 1.2.0.
		{"v1.2", "v1.2.0", false, true},
		{"v1.3", "v1.2.9", true, true},

		// A pre-release precedes the release it is heading for, so the shipped
		// 0.1.0 IS an update for a 0.1.0-dev build — this is the default build's
		// own case, and the one most likely to be got backwards.
		{"v0.1.0", "0.1.0-dev", true, true},
		{"v0.1.0-rc.1", "0.1.0", false, true},
		// Two pre-releases at the same numbers are deliberately not ordered.
		{"v0.1.0-rc.2", "0.1.0-dev", false, true},
		// Build metadata is not a pre-release, so it does not lower the version.
		{"v0.1.0", "0.1.0+abc1234", false, true},

		// Unreadable on either side must produce no claim at all.
		{"nightly", "v1.2.3", false, false},
		{"v1.2.3", "handbuilt", false, false},
		{"v1.2.3.4", "v1.2.3", false, false},
		{"v1.2.x", "v1.2.3", false, false},
		{"", "v1.2.3", false, false},
		{"v1.2.3", "", false, false},
	}
	for _, c := range cases {
		newer, ok := versionIsNewer(c.latest, c.current)
		if newer != c.newer || ok != c.ok {
			t.Errorf("versionIsNewer(%q, %q) = %v, %v; want %v, %v",
				c.latest, c.current, newer, ok, c.newer, c.ok)
		}
	}
}

// TestCheckForUpdateOffMakesNoClaim is the privacy charter as a test: with the
// setting off, the answer must arrive without a network call and must not carry
// a latest version, an update verdict, or a link. The nil ctx is the proof — a
// fetch would panic on it, so reaching the network here fails loudly.
func TestCheckForUpdateOffMakesNoClaim(t *testing.T) {
	a := newTestApp()
	a.cfg.UI.UpdateCheck = false

	info, err := a.CheckForUpdate()
	if err != nil {
		t.Fatalf("CheckForUpdate with the check off: %v", err)
	}
	if info.Enabled {
		t.Error("Enabled is true with the setting off")
	}
	if info.Latest != "" || info.URL != "" || info.Newer || info.CheckedMs != 0 {
		t.Errorf("an off check answered with content: %+v", info)
	}
	if info.Current == "" {
		t.Error("Current is empty; this build's own version is known either way")
	}
}
