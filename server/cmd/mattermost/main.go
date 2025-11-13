// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"os"

	"github.com/mattermost/mattermost/server/v8/cmd/mattermost/commands"
	// Import and register app layer slash commands
	_ "github.com/mattermost/mattermost/server/v8/channels/app/slashcommands"
	// Plugins
	_ "github.com/mattermost/mattermost/server/v8/channels/app/oauthproviders/gitlab"
	_ "github.com/mattermost/mattermost/server/v8/channels/app/oauthproviders/openid"

	// Enterprise Imports
	_ "github.com/mattermost/mattermost/server/v8/enterprise"

	// HA cluster registration
	_ "github.com/mattermost/mattermost/server/v8/cluster/register"

	godotenv "github.com/joho/godotenv"
)

func main() {
	godotenv.Load()
	if err := commands.Run(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}
