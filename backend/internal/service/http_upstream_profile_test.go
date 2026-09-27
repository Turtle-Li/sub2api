package service

import (
	"context"
	"testing"
)

func TestWithHTTPUpstreamProfile_DefaultKeepsContext(t *testing.T) {
	ctx := context.Background()
	got := WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileDefault)
	if got != ctx {
		t.Fatal("default profile should not wrap context")
	}
}

func TestWithHTTPUpstreamProfile_OpenAI(t *testing.T) {
	ctx := WithHTTPUpstreamProfile(context.TODO(), HTTPUpstreamProfileOpenAI)
	if profile := HTTPUpstreamProfileFromContext(ctx); profile != HTTPUpstreamProfileOpenAI {
		t.Fatalf("expected profile %q, got %q", HTTPUpstreamProfileOpenAI, profile)
	}
}

func TestWithHTTPUpstreamProfile_LongStream(t *testing.T) {
	ctx := WithHTTPUpstreamProfile(context.TODO(), HTTPUpstreamProfileLongStream)
	if profile := HTTPUpstreamProfileFromContext(ctx); profile != HTTPUpstreamProfileLongStream {
		t.Fatalf("expected profile %q, got %q", HTTPUpstreamProfileLongStream, profile)
	}
}

func TestWithHTTPUpstreamRedirectsDisabled(t *testing.T) {
	//nolint:staticcheck // Exercises the defensive nil-context fallback.
	ctx := WithHTTPUpstreamRedirectsDisabled(nil)
	if !HTTPUpstreamRedirectsDisabled(ctx) {
		t.Fatal("expected redirects to be disabled")
	}
	if HTTPUpstreamRedirectsDisabled(context.Background()) {
		t.Fatal("redirects should remain enabled by default")
	}
}

func TestWithHTTPUpstreamResolvedIPValidation(t *testing.T) {
	ctx := WithHTTPUpstreamResolvedIPValidation(context.Background(), false)
	allowPrivate, forced := HTTPUpstreamResolvedIPValidation(ctx)
	if allowPrivate {
		t.Fatal("private destinations should be rejected by default")
	}
	if !forced {
		t.Fatal("expected resolved-IP validation marker to be forced")
	}

	ctx = WithHTTPUpstreamResolvedIPValidation(context.Background(), true)
	allowPrivate, forced = HTTPUpstreamResolvedIPValidation(ctx)
	if !allowPrivate || !forced {
		t.Fatalf("expected private opt-in marker, got allowPrivate=%v forced=%v", allowPrivate, forced)
	}
}

func TestWithHTTPUpstreamPublicHostsOnly(t *testing.T) {
	//nolint:staticcheck // Exercises the defensive nil-context fallback.
	ctx := WithHTTPUpstreamPublicHostsOnly(nil)
	if !HTTPUpstreamPublicHostsOnly(ctx) {
		t.Fatal("expected public-hosts-only marker to be set")
	}
	if HTTPUpstreamPublicHostsOnly(context.Background()) {
		t.Fatal("marker must be absent by default")
	}
	if HTTPUpstreamRedirectsDisabled(ctx) {
		t.Fatal("public-hosts-only must not disable redirects")
	}
}
