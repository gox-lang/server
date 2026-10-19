package ls

import (
	"context"
	"fmt"
	"os"

	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/selfupdate"
)

func watchUpdates(ctx context.Context, notify func(string, any) error) {
	manager, err := selfupdate.New(buildinfo.Version())
	if err != nil {
		return
	}
	manager.Watch(ctx, func(release selfupdate.Release) {
		if !manager.Managed() || os.Getenv("GOX_LSP_AUTO_UPDATE") != "1" {
			_ = notify("window/logMessage", map[string]any{"type": 3, "message": fmt.Sprintf("GOX %s is available. Run gox update to install it.", release.Version)})
			return
		}
		if err := manager.Install(ctx, release); err != nil {
			_ = notify("window/logMessage", map[string]any{"type": 2, "message": fmt.Sprintf("GOX update could not be installed: %v. The current version remains active.", err)})
			return
		}
		_ = notify("gox/updateReady", map[string]string{"version": release.Version})
	})
}
