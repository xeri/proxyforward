package main

// Milestone-1 risk spike: Wails v2 has no built-in system tray, so the GUI
// plan depends on energye/systray coexisting with Wails' message loop on
// Windows. This hidden command exercises systray standalone (message loop,
// menu, clean Quit) so the go/no-go call is made before any GUI work.

import (
	"fmt"
	"time"

	"github.com/energye/systray"
	"github.com/spf13/cobra"

	"proxyforward/internal/wincon"
)

func newTraySpikeCmd() *cobra.Command {
	var holdSeconds int
	cmd := &cobra.Command{
		Use:    "tray-spike",
		Short:  "Internal: verify the system tray integration works on this machine",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			wincon.AttachParent()
			fmt.Printf("tray-spike: showing tray item for %ds...\n", holdSeconds)
			start := time.Now()
			systray.Run(func() {
				systray.SetTooltip("proxyforward tray spike")
				item := systray.AddMenuItem("proxyforward (spike)", "spike menu entry")
				item.Disable()
				quit := systray.AddMenuItem("Quit", "exit the spike")
				quit.Click(func() { systray.Quit() })
				time.AfterFunc(time.Duration(holdSeconds)*time.Second, systray.Quit)
			}, func() {
				fmt.Printf("tray-spike: exited cleanly after %s\n", time.Since(start).Round(time.Millisecond))
			})
			return nil
		},
	}
	cmd.Flags().IntVar(&holdSeconds, "hold", 3, "seconds to keep the tray item alive")
	return cmd
}
