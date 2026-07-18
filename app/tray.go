package app

// The system tray: the half of the power-state model that outlives the window.
// A persisted Stop's footgun is a forgotten stop, and the stopped banner only
// helps while the window is open — the icon carries runState permanently.
//
// Threading: systray's init() locks the main goroutine to the main OS thread,
// and Register creates the tray's hidden window on the calling thread — so
// RegisterTray runs in runGUI, before wails.Run, and Wails' own message pump
// services the tray window from then on. Menu callbacks therefore fire
// *inside* that pump: every handler dispatches to a goroutine and never
// blocks (StopEngine alone can wait ten seconds).

import (
	"fmt"
	"sync"

	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"proxyforward/internal/engine"
	"proxyforward/internal/trayicon"
)

// trayState is what the icon and menu communicate; two equal states render
// identically, which is what makes the 2 Hz reconcile cheap.
type trayState struct {
	icon    trayicon.State
	players int
}

type tray struct {
	a     *App
	icons map[trayicon.State][]byte

	mu     sync.Mutex // guards the fields below; onReady runs concurrently with ticks
	last   trayState
	seeded bool
	header *systray.MenuItem // disabled status line
	power  *systray.MenuItem // Stop tunnel / Start tunnel
}

// RegisterTray puts the icon in the tray. Called on the main goroutine before
// wails.Run whenever no daemon answered the pipe — including wizard mode: the
// rule is "the tray exists exactly when closing would kill the tunnel", and a
// wizard becomes an engine mid-session while registration cannot wait (it is
// main-thread-only). A grey icon during setup beats a missing one after it.
// Render failure just means no tray: closeDecision sees trayActive false and
// close falls back to quitting, never to hiding into nowhere. A package-level
// function, not a method: methods on App become frontend bindings.
func RegisterTray(a *App, markPNG []byte) {
	t := &tray{a: a, icons: map[trayicon.State][]byte{}}
	for _, st := range []trayicon.State{trayicon.StateRunning, trayicon.StateStopped, trayicon.StateFatal} {
		ico, err := trayicon.Render(markPNG, st)
		if err != nil {
			a.logger.Error("tray icon render failed; running without a tray", "err", err)
			return
		}
		t.icons[st] = ico
	}
	a.tray = t
	systray.Register(t.onReady, nil)
}

func (t *tray) onReady() {
	systray.SetIcon(t.icons[trayicon.StateStopped])
	systray.SetTooltip("proxyforward")
	t.mu.Lock()
	t.header = systray.AddMenuItem("proxyforward", "")
	t.header.Disable()
	open := systray.AddMenuItem("Open proxyforward", "Show the window")
	systray.AddSeparator()
	t.power = systray.AddMenuItem("Stop tunnel", "")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit proxyforward", "Stop the tunnel and exit")
	t.mu.Unlock()

	open.Click(func() { go t.a.surfaceWindow() })
	quit.Click(func() { go t.a.trayQuit() })
	t.power.Click(func() { go t.a.trayPower() })
	// Left-click (and double-click) surface the window; right-click keeps the
	// default menu behavior.
	systray.SetOnClick(func(systray.IMenu) { go t.a.surfaceWindow() })
	systray.SetOnDClick(func(systray.IMenu) { go t.a.surfaceWindow() })

	t.a.mu.Lock()
	t.a.trayActive = true
	t.a.mu.Unlock()
}

// trayUpdate reconciles the icon, tooltip, and menu with the 2 Hz status
// snapshot. Updates only fire on change — SetIcon round-trips through a temp
// file inside the systray library, so churn is not free.
func (a *App) trayUpdate(st UIStatus) {
	t := a.tray
	if t == nil {
		return
	}
	a.mu.Lock()
	active := a.trayActive
	a.mu.Unlock()
	if !active {
		return
	}

	next := trayState{icon: trayicon.StateStopped, players: uiPlayers(st)}
	switch {
	case st.EngineFatal != "":
		next.icon = trayicon.StateFatal
	case st.RunState == string(engine.RunRunning) || st.RunState == string(engine.RunStopping):
		next.icon = trayicon.StateRunning
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.header == nil || t.power == nil {
		return // onReady hasn't built the menu yet
	}
	if t.seeded && next == t.last {
		return
	}
	if !t.seeded || next.icon != t.last.icon {
		systray.SetIcon(t.icons[next.icon])
	}
	label, tip := trayCopy(next)
	t.header.SetTitle(label)
	systray.SetTooltip(tip)
	if next.icon == trayicon.StateRunning {
		t.power.SetTitle("Stop tunnel")
	} else {
		t.power.SetTitle("Start tunnel")
	}
	t.last, t.seeded = next, true
}

// uiPlayers is the honest connection count: the clamped list undercounts on
// busy gateways, so prefer the total the clamp reports alongside it.
func uiPlayers(st UIStatus) int {
	if st.ConnectionsTotal > 0 {
		return st.ConnectionsTotal
	}
	return len(st.Connections)
}

// trayCopy names the state for the menu header and the hover tooltip.
func trayCopy(s trayState) (header, tooltip string) {
	switch s.icon {
	case trayicon.StateFatal:
		return "Tunnel crashed — open for details", "proxyforward — crashed"
	case trayicon.StateRunning:
		switch {
		case s.players == 1:
			return "Running — 1 player connected", "proxyforward — running · 1 player"
		case s.players > 1:
			return fmt.Sprintf("Running — %d players connected", s.players),
				fmt.Sprintf("proxyforward — running · %d players", s.players)
		}
		return "Running", "proxyforward — running"
	default:
		return "Tunnel stopped", "proxyforward — stopped"
	}
}

// trayPower is the menu's Stop/Start. Stop with players connected surfaces the
// window and routes through the same named-count confirm as every other verb
// that drops players; an empty tunnel stops at once.
func (a *App) trayPower() {
	st := a.Status()
	if st.EngineFatal != "" || st.RunState != string(engine.RunRunning) {
		if err := a.StartEngine(); err != nil {
			a.logger.Warn("tray start", "err", err)
		}
		return
	}
	if players := uiPlayers(st); players > 0 {
		a.surfaceWindow()
		runtime.EventsEmit(a.ctx, "pxf:close", closePrompt{Kind: "stop", Players: players})
		return
	}
	if err := a.StopEngine(false); err != nil {
		a.logger.Warn("tray stop", "err", err)
	}
}

// trayQuit is the menu's Quit: the one tray verb that also drops the window.
// With players connected it surfaces the window for the named-count confirm;
// empty, it quits at once.
func (a *App) trayQuit() {
	st := a.Status()
	players := uiPlayers(st)
	if players > 0 && st.EngineFatal == "" && st.RunState == string(engine.RunRunning) {
		a.surfaceWindow()
		runtime.EventsEmit(a.ctx, "pxf:close", closePrompt{Kind: "askquit", Players: players})
		return
	}
	a.QuitAndDisconnect()
}

// teardownTray removes the icon while the message pump still runs — after the
// pump exits nobody processes the WM_CLOSE, and Explorer keeps a ghost icon
// until the next hover. Safe to call more than once (systray.Quit is a Once).
func (a *App) teardownTray() {
	a.mu.Lock()
	active := a.trayActive
	a.trayActive = false
	a.mu.Unlock()
	if active {
		systray.Quit()
	}
}
