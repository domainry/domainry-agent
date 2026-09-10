// Package web delegates the browser transport to the Agent SDK.
package web

import (
	gateway "github.com/domainry/domainry-agent-sdk/browsergateway"
	"net/http"
)

type Options = gateway.Options

func NewHandler(options Options) (http.Handler, error) { return gateway.NewHandler(options) }
func Scope(runtimeID, workspaceID, userID string) string {
	return gateway.Scope(runtimeID, workspaceID, userID)
}
