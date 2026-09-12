package web

import (
	"context"
	"path/filepath"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/businessrpc"
	identity "github.com/domainry/domainry-identity-sdk"
)

type borrowedIdentityProbe struct {
	identity.Binding
	registries, closes int
}

func (p *borrowedIdentityProbe) Permissions() identity.PermissionRegistry {
	p.registries++
	return p.Binding.Permissions()
}
func (p *borrowedIdentityProbe) Applications() identity.ApplicationRegistry {
	p.registries++
	return p.Binding.Applications()
}
func (p *borrowedIdentityProbe) Close(context.Context) error { p.closes++; return nil }

func TestBorrowedIdentityDoesNotPublishOrCloseOwner(t *testing.T) {
	f := newToolSettingsFixture(t)
	owner, options := f.host, f.options
	options.Integration = owner.Integration
	if owner.ToolSettings == nil || options.Integration == nil {
		t.Fatal("real Tools and Integration owners are required")
	}
	reader := owner.Identity.Permissions().(identity.PermissionSnapshotReader)
	request := identity.PermissionSourceSnapshotRequest{Application: owner.application, SourceOwner: "agent:conversation_tools"}
	before, err := reader.CurrentSourceSnapshot(t.Context(), request)
	if err != nil || len(before.Definitions) == 0 {
		t.Fatal("owner permission snapshot", err)
	}
	probe := &borrowedIdentityProbe{Binding: owner.Identity}
	options.DatabasePath = filepath.Join(t.TempDir(), "consumer.db")
	options.IdentityBinding = probe
	options.CalendarWriteTools, options.MailWriteTools = true, true
	options.ReportTools = true
	options.KnowledgePermissions = map[string]string{"finance": "upstream-finance"}
	consumer, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if consumer.Identity.Descriptor().Issuer != owner.Identity.Descriptor().Issuer || consumer.knowledgePermissions["finance"] != "upstream-finance" {
		t.Fatal("borrowed Identity or local permission mapping changed")
	}
	if err := consumer.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if probe.registries != 0 || probe.closes != 0 {
		t.Fatalf("consumer took owner responsibilities: registries=%d closes=%d", probe.registries, probe.closes)
	}
	after, err := reader.CurrentSourceSnapshot(t.Context(), request)
	if err != nil || after.SnapshotHash != before.SnapshotHash {
		t.Fatal("owner snapshot or lifecycle changed", err)
	}
	// A bad shared scope must fail without closing the caller's owner either.
	options.DatabasePath = filepath.Join(t.TempDir(), "rejected.db")
	options.ApplicationKey = "other-app"
	if _, err := Open(t.Context(), options); err == nil || probe.closes != 0 {
		t.Fatal("mismatched shared audience accepted or owner closed", err)
	}
}

// Only descriptor validation is under test here. Actual business transport and
// owner effects are covered by SDK HTTP and Runtime owner integration tests.
type describedBusinessSource struct {
	agent.ConversationBusinessSource
	descriptor businessrpc.Descriptor
}

func (s *describedBusinessSource) Descriptor() businessrpc.Descriptor { return s.descriptor }
func (s *describedBusinessSource) BusinessSourceIdentity() string     { return s.descriptor.SourceIdentity }

type descriptorIdentity struct {
	identity.Binding
	descriptor identity.Descriptor
}

func (i descriptorIdentity) Descriptor() identity.Descriptor { return i.descriptor }

func TestBusinessServiceRequiresSharedIdentityAndExactScope(t *testing.T) {
	d := businessrpc.Descriptor{ProtocolVersion: businessrpc.ProtocolVersion, ContractSHA256: businessrpc.ContractSHA256(), SourceIdentity: "source", Scope: businessrpc.Scope{RuntimeID: "r", WorkspaceID: "w", ApplicationKey: "a", IdentityIssuer: "https://identity.example.test"}}
	for _, change := range []struct {
		name   string
		mutate func(*Host, *describedBusinessSource)
	}{
		{"local_identity", func(h *Host, _ *describedBusinessSource) { h.identityBorrowed = false }},
		{"runtime", func(_ *Host, s *describedBusinessSource) { s.descriptor.Scope.RuntimeID = "other" }},
		{"workspace", func(_ *Host, s *describedBusinessSource) { s.descriptor.Scope.WorkspaceID = "other" }},
		{"application", func(_ *Host, s *describedBusinessSource) { s.descriptor.Scope.ApplicationKey = "other" }},
		{"issuer", func(_ *Host, s *describedBusinessSource) {
			s.descriptor.Scope.IdentityIssuer = "https://other.example.test"
		}},
		{"missing_issuer", func(_ *Host, s *describedBusinessSource) { s.descriptor.Scope.IdentityIssuer = "" }},
		{"contract", func(_ *Host, s *describedBusinessSource) { s.descriptor.ContractSHA256 = "old" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			source := &describedBusinessSource{descriptor: d}
			h := &Host{runtimeID: "r", application: identity.ApplicationRef{WorkspaceID: "w", ApplicationKey: "a"}, identityBorrowed: true, Identity: descriptorIdentity{descriptor: identity.Descriptor{Issuer: d.Scope.IdentityIssuer}}, businessSource: source}
			if err := h.validateBusinessBinding(); err != nil || h.ConversationBusinessSource() != source {
				t.Fatal("valid source rejected", err)
			}
			change.mutate(h, source)
			if err := h.validateBusinessBinding(); err == nil {
				t.Fatal("incompatible business binding accepted")
			}
		})
	}
}
