package app

import "testing"

// TestCloseDecisionMatrix is the power-state plan's "Confirmations" table,
// plus the rules the table implies: attached mode never prompts (closing
// disconnects nobody), a confirmed quit is never re-asked, and hiding
// requires the tray to actually exist.
func TestCloseDecisionMatrix(t *testing.T) {
	cases := []struct {
		name                               string
		mode                               string
		players                            int
		trayOn, noticeShown, quitConfirmed bool
		want                               closeVerdict
	}{
		{"attached always quits", ModeAttached, 5, true, true, false, closeQuit},
		{"wizard quits", ModeWizard, 0, true, true, false, closeQuit},
		{"confirmed quit passes", ModeEngine, 5, true, true, true, closeQuit},
		{"nobody on, tray off: plain quit", ModeEngine, 0, false, true, false, closeQuit},
		{"nobody on, tray on: hide", ModeEngine, 0, true, true, false, closeHide},
		{"first hide shows the notice", ModeEngine, 0, true, false, false, closeNotice},
		{"players on, tray on: fork", ModeEngine, 3, true, true, false, closeAskTray},
		{"players on, tray off: confirm", ModeEngine, 3, false, true, false, closeAskQuit},
		{"players on, attached: quit", ModeAttached, 3, false, true, false, closeQuit},
		{"players on, tray on, notice unseen: still the fork", ModeEngine, 2, true, false, false, closeAskTray},
	}
	for _, c := range cases {
		got := closeDecision(c.mode, c.players, c.trayOn, c.noticeShown, c.quitConfirmed)
		if got != c.want {
			t.Errorf("%s: closeDecision = %v, want %v", c.name, got, c.want)
		}
	}
}
