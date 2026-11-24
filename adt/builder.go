package adt

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/Hochfrequenz/adtler/auth"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config/v2"
)

// NewClientsFromConfig builds one Client per system in cfg, ready to pass to
// NewClientRegistry. For OAuth2 systems it loads the stored token via the
// auth package's TokenStore (see auth.DefaultTokenPath) and wires up automatic
// refresh on 401 responses.
//
// A system that cannot be built (an OAuth2 system without a stored token or
// without a client ID) is left out of the map, and its error is joined into
// the returned error. The map still holds the clients of the other systems, so
// a caller may continue with them. On total failure the map is empty but not
// nil. A caller that continues must keep the default system in the map, because
// NewClientRegistry fails when the default system is missing or the map is empty.
// A caller that returns on err != nil fails fast as before.
//
// defaultOAuth2ClientID is used when a system's own OAuth2ClientID is empty;
// pass "" to require explicit per-system configuration.
func NewClientsFromConfig(cfg *sapmcpconfig.Config, defaultOAuth2ClientID string) (map[string]Client, error) {
	store := auth.NewTokenStore(auth.DefaultTokenPath())
	clients := make(map[string]Client, len(cfg.Systems))
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(cfg.Systems)) {
		sysCfg := cfg.Systems[name]
		if !sysCfg.IsOAuth2() {
			clients[name] = NewClient(sysCfg)
			continue
		}
		tokenData, err := store.TokenForSystem(name)
		if err != nil {
			errs = append(errs, fmt.Errorf("system %q requires OAuth2 login — no stored token found: %w", name, err))
			continue
		}
		clientID := effectiveOAuth2ClientID(sysCfg, defaultOAuth2ClientID)
		if clientID == "" {
			errs = append(errs, fmt.Errorf("system %q: OAuth2 requires oauth2_client_id in config or a default client ID", name))
			continue
		}
		// td is written without a lock. That is safe only because this callback
		// belongs to exactly one client: its tokenSource, shared with every
		// freshSession clone, serializes the calls (issue #197). Do not hand
		// onRefresh to a second client.
		td := tokenData // closure-mutable copy of the latest known token
		onRefresh := func(_ string) (string, error) {
			newToken, err := auth.RefreshToken(
				sysCfg.Host,
				clientID,
				td.RefreshToken,
				sysCfg.TLSSkipVerify,
			)
			if err != nil {
				return "", fmt.Errorf("token refresh failed for %q: %w", name, err)
			}
			_ = store.Save(name, newToken)
			td = newToken
			return newToken.AccessToken, nil
		}
		clients[name] = NewClientWithToken(sysCfg, tokenData.AccessToken, onRefresh)
	}
	return clients, errors.Join(errs...)
}

// effectiveOAuth2ClientID returns the OAuth2 client ID for the given system,
// preferring the system's own OAuth2ClientID, then falling back to the provided default.
func effectiveOAuth2ClientID(sys sapmcpconfig.SAPSystem, fallback string) string {
	if sys.OAuth2ClientID != "" {
		return sys.OAuth2ClientID
	}
	return fallback
}
