package web

import (
	"fmt"

	"github.com/domainry/domainry-agent-sdk/businessrpc"
	identity "github.com/domainry/domainry-identity-sdk"
	identityapplication "github.com/domainry/domainry-identity-sdk/application"
)

func bindSharedIdentity(binding identity.Binding, application identity.ApplicationRef) (identity.Binding, error) {
	// Reuse the SDK's scope enforcement, including audience validation. Binding
	// does not transfer lifecycle ownership from the caller to the web host.
	return identityapplication.Bind(binding, application)
}

func (h *Host) validateBusinessBinding() error {
	remote, ok := h.businessSource.(interface{ Descriptor() businessrpc.Descriptor })
	if !ok {
		return nil // a locally supplied Source retains the existing Module contract
	}
	d := remote.Descriptor()
	if !h.identityBorrowed || d.ProtocolVersion != businessrpc.ProtocolVersion || d.ContractSHA256 != businessrpc.ContractSHA256() ||
		d.Scope.RuntimeID != h.runtimeID || d.Scope.WorkspaceID != string(h.application.WorkspaceID) || d.Scope.ApplicationKey != string(h.application.ApplicationKey) ||
		d.Scope.IdentityIssuer == "" || d.Scope.IdentityIssuer != h.Identity.Descriptor().Issuer || d.SourceIdentity != h.businessSource.BusinessSourceIdentity() {
		return fmt.Errorf("business service requires the same scoped Identity binding and compatible source")
	}
	return nil
}
