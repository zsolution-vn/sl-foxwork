package register

import (
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/v8/channels/app/platform"
	"github.com/mattermost/mattermost/server/v8/cluster/ha"
	"github.com/mattermost/mattermost/server/v8/einterfaces"
)

// init registers the HA cluster implementation with the platform.
func init() {
	platform.RegisterClusterInterface(func(ps *platform.PlatformService) einterfaces.ClusterInterface {
		cfg := ha.ConfigFromPlatform(ps)
		if !cfg.Enabled {
			ps.Log().Debug("Cluster disabled by config; returning nil cluster interface")
			return nil
		}
		c, err := ha.New(ps, cfg)
		if err != nil {
			ps.Log().Error("Failed to initialize HA cluster", mlog.Err(err))
			return nil
		}
		return c
	})
}


