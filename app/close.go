package app

// Closing the window is a fork, not a kill: when this process owns the run,
// the X button minimizes to the tray (the tunnel outlives the window), and
// every path that would drop players names the count first. One pure decision
// function picks the outcome; the dialogs render in the frontend off a
// pxf:close event, because the confirm must be able to say "3 players".
//
// Every quit path funnels through here: the native close button, Alt-F4, and
// the frontend's runtime Quit() all reach Wails' Frontend.Quit, which asks
// OnBeforeClose first.

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type closeVerdict int

const (
	closeQuit    closeVerdict = iota // let the quit proceed
	closeHide                        // hide to the tray, silently
	closeNotice                      // hide, but show the one-time tray notice first
	closeAskTray                     // players on, tray on: Minimize / Quit and disconnect
	closeAskQuit                     // players on, no tray: Quit and disconnect / Cancel
)

// closePrompt is the pxf:close payload; Kind is one of notice|asktray|askquit
// (from BeforeClose) or stop (from the tray menu's Stop, reusing the same
// dialog surface).
type closePrompt struct {
	Kind    string `json:"kind"`
	Players int    `json:"players"`
}

// closeDecision is the whole close policy, pure so the confirm matrix is a
// table test. trayOn means the preference is on AND the icon actually exists —
// hiding without an icon would strand the app invisible, so the caller passes
// trayActive && cfg. Outside engine mode closing disconnects nobody (the run
// is a daemon's, or there is none), so it never prompts.
func closeDecision(mode string, players int, trayOn, noticeShown, quitConfirmed bool) closeVerdict {
	if quitConfirmed || mode != ModeEngine {
		return closeQuit
	}
	if !trayOn {
		if players > 0 {
			return closeAskQuit
		}
		return closeQuit
	}
	if players > 0 {
		return closeAskTray
	}
	if !noticeShown {
		return closeNotice
	}
	return closeHide
}

// BeforeClose is wired to Wails OnBeforeClose. Returning true swallows the
// close; a swallowed close either hid the window or handed the decision to the
// frontend dialog, whose buttons call HideToTray / QuitAndDisconnect.
func (a *App) BeforeClose(ctx context.Context) bool {
	st := a.Status()
	players := uiPlayers(st)
	a.mu.Lock()
	mode := a.mode
	trayOn := a.trayActive && a.cfg.UI.MinimizeToTray
	noticeShown := a.cfg.UI.TrayNoticeShown
	quitConfirmed := a.quitConfirmed
	a.mu.Unlock()

	switch closeDecision(mode, players, trayOn, noticeShown, quitConfirmed) {
	case closeHide:
		runtime.WindowHide(a.ctx)
		return true
	case closeNotice:
		runtime.EventsEmit(a.ctx, "pxf:close", closePrompt{Kind: "notice", Players: players})
		return true
	case closeAskTray:
		runtime.EventsEmit(a.ctx, "pxf:close", closePrompt{Kind: "asktray", Players: players})
		return true
	case closeAskQuit:
		runtime.EventsEmit(a.ctx, "pxf:close", closePrompt{Kind: "askquit", Players: players})
		return true
	default:
		// Remove the icon while the pump still runs, or Explorer keeps a
		// ghost until the next hover.
		a.teardownTray()
		return false
	}
}

// HideToTray hides the window on the user's say-so (a dialog's Minimize) and
// permanently marks the tray notice as seen — the notice exists to be shown
// once, and answering any tray dialog proves the tray is understood.
func (a *App) HideToTray() {
	a.mu.Lock()
	if !a.cfg.UI.TrayNoticeShown {
		a.cfg.UI.TrayNoticeShown = true
		if err := a.cfg.Save(a.configPath); err != nil {
			a.logger.Warn("persist tray notice flag", "err", err)
		}
	}
	a.mu.Unlock()
	runtime.WindowHide(a.ctx)
}

// QuitAndDisconnect quits for real — the disconnect confirm (if any) already
// happened in the UI, so BeforeClose must not re-ask.
func (a *App) QuitAndDisconnect() {
	a.mu.Lock()
	a.quitConfirmed = true
	a.mu.Unlock()
	runtime.Quit(a.ctx)
}
