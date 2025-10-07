// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package oauthopenid

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/public/shared/request"
	"github.com/mattermost/mattermost/server/v8/einterfaces"
)

// OpenIDProvider implements a generic OpenID Connect provider using the standard UserInfo response.
type OpenIDProvider struct{}

// openIDUser represents a minimal subset of fields from OIDC UserInfo response.
// See: https://openid.net/specs/openid-connect-core-1_0.html#StandardClaims
type openIDUser struct {
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	GivenName         string `json:"given_name"`
	FamilyName        string `json:"family_name"`
}

func init() {
	provider := &OpenIDProvider{}
	einterfaces.RegisterOAuthProvider(model.ServiceOpenid, provider)
	// Debug log: confirm provider registration at startup
	fmt.Println("[mattermost] OpenID provider registered")
}

func userFromOpenID(logger mlog.LoggerIFace, oi *openIDUser) (*model.User, error) {
	if oi.Subject == "" && oi.Email == "" {
		return nil, errors.New("openid user: both sub and email are empty")
	}
	logger.Debug("OpenID user", mlog.String("subject", oi.Subject), mlog.String("email", oi.Email), mlog.String("preferred_username", oi.PreferredUsername), mlog.String("name", oi.Name), mlog.String("given_name", oi.GivenName), mlog.String("family_name", oi.FamilyName))

	user := &model.User{}

	// Username preference: email local-part -> preferred_username -> sub
	// Always try to base username on email local-part when available.
	username := ""
	if oi.Email != "" {
		at := strings.Index(oi.Email, "@")
		if at > 0 {
			username = oi.Email[:at]
		}
	}
	if username == "" && oi.PreferredUsername != "" {
		username = oi.PreferredUsername
	}
	if username == "" {
		username = oi.Subject
	}
	user.Username = model.CleanUsername(logger, username)

	if oi.Email != "" {
		user.Email = strings.ToLower(oi.Email)
	} else {
		// Fallback synthetic email to satisfy Mattermost constraints
		user.Email = strings.ToLower("oidc_" + oi.Subject + "@oidc.local")
	}

	// Name mapping
	if oi.GivenName != "" || oi.FamilyName != "" {
		user.FirstName = oi.GivenName
		user.LastName = oi.FamilyName
	} else if oi.Name != "" {
		// Best-effort split
		parts := strings.Fields(oi.Name)
		if len(parts) >= 2 {
			user.FirstName = parts[0]
			user.LastName = strings.Join(parts[1:], " ")
		} else {
			user.FirstName = oi.Name
		}
	}

	// Auth linkage: always use email as AuthData (lowercased). If email missing,
	// fallback to the synthetic email set above so it's stable.
	authData := user.Email
	user.AuthData = &authData
	user.AuthService = model.ServiceOpenid

	return user, nil
}

func decodeOpenIDUser(data io.Reader) (*openIDUser, error) {
	dec := json.NewDecoder(data)
	var oi openIDUser
	if err := dec.Decode(&oi); err != nil {
		return nil, err
	}
	return &oi, nil
}

// GetUserFromJSON builds a Mattermost user from an OIDC UserInfo JSON payload.
func (op *OpenIDProvider) GetUserFromJSON(rctx request.CTX, data io.Reader, tokenUser *model.User) (*model.User, error) {
	oi, err := decodeOpenIDUser(data)
	if err != nil {
		return nil, err
	}
	return userFromOpenID(rctx.Logger(), oi)
}

// GetSSOSettings returns OpenId settings from config.
// Discovery resolution (well-known) is expected to populate endpoints into config beforehand
// via admin UI or separate bootstrap logic.
func (op *OpenIDProvider) GetSSOSettings(_ request.CTX, config *model.Config, service string) (*model.SSOSettings, error) {
	// If endpoints already present, return as-is.
	if config.OpenIdSettings.AuthEndpoint != nil && *config.OpenIdSettings.AuthEndpoint != "" &&
		config.OpenIdSettings.TokenEndpoint != nil && *config.OpenIdSettings.TokenEndpoint != "" &&
		config.OpenIdSettings.UserAPIEndpoint != nil && *config.OpenIdSettings.UserAPIEndpoint != "" {
		return &config.OpenIdSettings, nil
	}

	// Resolve from DiscoveryEndpoint if available.
	if config.OpenIdSettings.DiscoveryEndpoint == nil || *config.OpenIdSettings.DiscoveryEndpoint == "" {
		return &config.OpenIdSettings, nil
	}

	discoveryURL := *config.OpenIdSettings.DiscoveryEndpoint
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(discoveryURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("oidc discovery failed: %s", resp.Status)
	}

	var doc struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		UserinfoEndpoint      string `json:"userinfo_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}

	// Make a copy and fill endpoints when missing.
	sso := config.OpenIdSettings
	if sso.AuthEndpoint == nil || *sso.AuthEndpoint == "" {
		sso.AuthEndpoint = model.NewPointer(doc.AuthorizationEndpoint)
	}
	if sso.TokenEndpoint == nil || *sso.TokenEndpoint == "" {
		sso.TokenEndpoint = model.NewPointer(doc.TokenEndpoint)
	}
	if sso.UserAPIEndpoint == nil || *sso.UserAPIEndpoint == "" {
		sso.UserAPIEndpoint = model.NewPointer(doc.UserinfoEndpoint)
	}
	return &sso, nil
}

// GetUserFromIdToken is optional for OIDC flow here; server will still call UserInfo endpoint.
func (op *OpenIDProvider) GetUserFromIdToken(_ request.CTX, idToken string) (*model.User, error) {
	return nil, nil
}

func (op *OpenIDProvider) IsSameUser(_ request.CTX, dbUser, oauthUser *model.User) bool {
	// Consider users the same if emails match (case-insensitive),
	// or if AuthData matches as a fallback.
	if strings.EqualFold(dbUser.Email, oauthUser.Email) && dbUser.Email != "" && oauthUser.Email != "" {
		return true
	}
	return dbUser.AuthData == oauthUser.AuthData
}
