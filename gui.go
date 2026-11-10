//go:build !linux

package main

import (
	"embed"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	wailslogger "github.com/wailsapp/wails/v2/pkg/logger"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailswin "github.com/wailsapp/wails/v2/pkg/options/windows"

	"proxyforward/app"
	"proxyforward/internal/config"
	"proxyforward/internal/logging"
)

// The GUI is built for Windows (and macOS); Linux runs headless, see gui_linux.go.
// Only this file embeds the frontend, so a linux build needs no frontend/dist.

//go:embed all:frontend/dist
var assets embed.FS

// runGUI opens the window: the bare `proxyforward` launch, and the target of a
// pxf:// protocol launch (deepLink, empty otherwise).
func runGUI(deepLink string) error {
	configPath := config.DefaultPath(false)
	cfg, err := config.Load(configPath)
	if err != nil {
		// The GUI must still open on a broken config so the user can fix it;
		// start from defaults and surface the load error in the UI later.
		cfg = config.Default()
	}
	ring := logging.NewRing(2000)
	var filePath string
	if cfg.Logging.FileEnabled {
		filePath = logging.DefaultFilePath(config.DefaultDir(false))
	}
	logger, closeLogs, lerr := logging.New(logging.Options{
		Level:    cfg.Logging.Level,
		FilePath: filePath,
		Ring:     ring,
	})
	if lerr != nil {
		return lerr
	}
	defer closeLogs()
	if err != nil {
		logger.Error("config failed to load; using defaults", "err", err)
	}

	a := app.New(configPath, cfg, ring, logger)
	// Fit before the page loads, so the window never shows oversized. It rides
	// in Startup rather than wrapping it (see App.BeforeStartup).
	a.BeforeStartup = fitWindowToScreen
	// A cold start via the OS protocol handler: the frontend isn't listening yet,
	// so stash the link now for it to pull once it mounts (App.TakePendingDeepLink).
	if deepLink != "" {
		a.HandleDeepLink(deepLink)
	}
	// Wails reports webview/runtime failures through its own logger and can
	// os.Exit without returning an error; in a windowsgui process that output
	// is invisible, so route it to a file next to the crash log.
	wailsLog := wailslogger.NewFileLogger(filepath.Join(config.DefaultDir(false), "logs", "wails.log"))
	return wails.Run(&options.App{
		Title:     "proxyforward",
		Width:     windowWidth,
		Height:    windowHeight,
		MinWidth:  windowMinWidth,
		MinHeight: windowMinHeight,
		// One GUI window owns the pairing flow: a second launch (e.g. clicking
		// another pxf:// link) is forwarded here and exits, so the link always
		// lands in the running window instead of opening a duplicate.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "proxyforward-gui-single-instance",
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				a.OnSecondInstance(deepLinkArg(data.Args))
			},
		},
		// The window is frameless: the frontend draws its own title bar and
		// window controls. DWM decorations stay on (default) so the frameless
		// window keeps its drop shadow, rounded corners and Snap Layouts.
		Frameless: true,
		// The avatar handler serves /pf/avatar/ for paths the bundled assets
		// don't cover (player heads are fetched and cached at runtime).
		AssetServer:      &assetserver.Options{Assets: assets, Handler: a.AvatarHandler()},
		BackgroundColour: startupBackground(cfg.UI.Theme),
		Windows: &wailswin.Options{
			Theme: wailswin.SystemDefault,
		},
		OnStartup:          a.Startup,
		OnShutdown:         a.Shutdown,
		Bind:               []interface{}{a},
		Logger:             wailsLog,
		LogLevelProduction: wailslogger.ERROR,
	})
}

// startupBackground picks the pre-paint window colour so the first frame (and
// resize flash) matches the theme the frontend is about to render. "system"
// resolves in the frontend; dark is the safer first frame for it here. Alpha
// stays 255 (opaque); Wails' Windows backend rounds any non-zero alpha up to 255
// anyway.
func startupBackground(theme string) *options.RGBA {
	if theme == "light" {
		return &options.RGBA{R: 236, G: 238, B: 246, A: 255}
	}
	return &options.RGBA{R: 11, G: 13, B: 19, A: 255}
}
