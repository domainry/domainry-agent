//go:build external_identity

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	webassembly "github.com/domainry/domainry-agent/internal/assembly/web"
	bridgeconfig "github.com/domainry/domainry-identity-bridge/config"
	bridgemodule "github.com/domainry/domainry-identity-bridge/module"
	identity "github.com/domainry/domainry-identity-sdk"
)

func externalIdentityOptions() (webassembly.Options, error) {
	var options webassembly.Options
	path := os.Getenv("DOMAINRY_IDENTITY_BRIDGE_CONFIG_FILE")
	if path == "" {
		return options, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return options, err
	}
	defer file.Close()
	config, err := bridgeconfig.Load(file)
	if err != nil {
		return options, err
	}
	if config.ApplicationKey != env("AGENT_WEB_APPLICATION_KEY", "domainry-agent-web") {
		return options, fmt.Errorf("external account application key must match the Agent application")
	}
	if config.Browser == nil || config.Browser.Credential.Location != "cookie" {
		return options, fmt.Errorf("Agent browser requires external cookie configuration")
	}
	origin := env("AGENT_WEB_ORIGIN", "http://"+env("AGENT_WEB_ADDRESS", "127.0.0.1:8091"))
	allowed := false
	for _, value := range config.Browser.AllowedOrigins {
		if value == origin {
			allowed = true
		}
	}
	if !allowed {
		return options, fmt.Errorf("Agent public origin must be in external browser.allowed_origins")
	}
	roles, err := os.Open(os.Getenv("AGENT_WEB_EXTERNAL_ROLES_FILE"))
	if err != nil {
		return options, fmt.Errorf("AGENT_WEB_EXTERNAL_ROLES_FILE is required: %w", err)
	}
	defer roles.Close()
	decoder := json.NewDecoder(io.LimitReader(roles, 1024*1024))
	decoder.DisallowUnknownFields()
	var definitions []identity.ProjectRoleDefinition
	if err := decoder.Decode(&definitions); err != nil {
		return options, err
	}
	if len(definitions) == 0 || decoder.Decode(new(any)) != io.EOF {
		return options, fmt.Errorf("external role file must contain one nonempty role array")
	}
	options.ExternalIdentity = bridgemodule.NewFactory(path, bridgemodule.Options{})
	options.ExternalRoles = definitions
	return options, nil
}
