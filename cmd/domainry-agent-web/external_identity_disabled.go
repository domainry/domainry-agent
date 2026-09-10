//go:build !external_identity

package main

import (
	"fmt"
	webassembly "github.com/domainry/domainry-agent/internal/assembly/web"
	"os"
)

func externalIdentityOptions() (webassembly.Options, error) {
	if os.Getenv("DOMAINRY_IDENTITY_BRIDGE_CONFIG_FILE") != "" {
		return webassembly.Options{}, fmt.Errorf("external account login requires building with -tags external_identity and the bridge source module")
	}
	return webassembly.Options{}, nil
}
