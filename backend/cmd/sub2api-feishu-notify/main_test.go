package main

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/ent/securitysecret"
)

// The command calls repository.ProvideEnt directly. Ent-generated defaults are
// initialized only by the ent/runtime side-effect import in main.go; without it,
// security-secret bootstrap panics before this command can reach its synthetic
// notification path.
func TestEntRuntimeInitializesSecuritySecretBootstrapDefaults(t *testing.T) {
	if securitysecret.DefaultCreatedAt == nil {
		t.Fatal("SecuritySecret created_at default is uninitialized")
	}
	if securitysecret.DefaultUpdatedAt == nil {
		t.Fatal("SecuritySecret updated_at default is uninitialized")
	}
	if securitysecret.UpdateDefaultUpdatedAt == nil {
		t.Fatal("SecuritySecret updated_at update default is uninitialized")
	}
	if securitysecret.DefaultCreatedAt().IsZero() || securitysecret.DefaultUpdatedAt().IsZero() || securitysecret.UpdateDefaultUpdatedAt().IsZero() {
		t.Fatal("SecuritySecret bootstrap defaults returned zero timestamps")
	}
}
