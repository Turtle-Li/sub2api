package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxActivationValidatesClearsAndRunsBoundedOperations(t *testing.T) {
	values := validEnvironment(t, sandboxActivationProfile)
	requestSecret := values[requestPrivateEnv]
	webhookSecret := values[webhookPrivateEnv]
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	unset := func(name string) error { delete(values, name); return nil }
	identity := testIdentity(t)
	var calls []string
	operations := activationOperations{
		injectSub2Request: func(_ context.Context, target activationTarget, input []byte) error {
			if target.profile != sandboxActivationProfile || target.sub2Host != sandboxSub2Host || string(input) != requestSecret {
				t.Fatal("Sub2 sandbox injection did not receive its bounded target and validated Base64 private key")
			}
			calls = append(calls, "sub2-inject")
			return nil
		},
		injectPayWebhook: func(_ context.Context, target activationTarget, input []byte) error {
			if target.payIdentity != identity || string(input) != webhookSecret {
				t.Fatal("payment sandbox injection input mismatch")
			}
			calls = append(calls, "pay-inject")
			return nil
		},
		enrollPay: func(_ context.Context, target activationTarget, sql string) error {
			if target.profile != sandboxActivationProfile || !strings.Contains(sql, "app.sub2.sandbox") ||
				strings.Contains(sql, requestSecret) || strings.Contains(sql, webhookSecret) {
				t.Fatal("sandbox enrollment SQL was not public-only or scope-bound")
			}
			calls = append(calls, "pay-enroll")
			return nil
		},
		configureSub2: func(_ context.Context, target activationTarget, configuration string) error {
			if target.profile != sandboxActivationProfile || !strings.Contains(configuration, "UNIFIED_PAYMENT_ENABLED=true") ||
				!strings.Contains(configuration, returnURL) || strings.Contains(configuration, requestSecret) || strings.Contains(configuration, webhookSecret) {
				t.Fatal("sandbox runtime config was not public-only or scope-bound")
			}
			calls = append(calls, "sub2-config")
			return nil
		},
	}
	output := &bytes.Buffer{}
	code := run([]string{"--pay-identity", identity}, output, lookup, unset, operations)
	if code != 0 || output.String() != "SUB2_PAYMENT_SANDBOX_ACTIVATED\n" {
		t.Fatalf("activation result = %d %q", code, output.String())
	}
	if len(values) != 0 {
		t.Fatalf("injected environment was not cleared: %v", values)
	}
	if strings.Join(calls, ",") != "sub2-inject,pay-inject,pay-enroll,sub2-config" {
		t.Fatalf("activation call order = %v", calls)
	}
	if strings.Contains(output.String(), requestSecret) || strings.Contains(output.String(), webhookSecret) {
		t.Fatal("activation output exposed a private value")
	}
}

func TestLiveActivationInjectsOnlyAndEmitsPublicEnrollmentBundle(t *testing.T) {
	values := validEnvironment(t, liveActivationProfile)
	requestSecret := values[requestPrivateEnv]
	webhookSecret := values[webhookPrivateEnv]
	requestPublic := values[requestPublicEnv]
	webhookPublic := values[webhookPublicEnv]
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	unset := func(name string) error { delete(values, name); return nil }
	identity := testIdentity(t)
	var calls []string
	operations := activationOperations{
		injectSub2Request: func(_ context.Context, target activationTarget, input []byte) error {
			if target.profile != liveActivationProfile || target.sub2Host != liveAzureSub2Host || string(input) != requestSecret {
				t.Fatal("live Sub2 injection escaped its approved target or input boundary")
			}
			calls = append(calls, "sub2-inject")
			return nil
		},
		injectPayWebhook: func(_ context.Context, target activationTarget, input []byte) error {
			if target.profile != liveActivationProfile || target.payIdentity != identity || string(input) != webhookSecret {
				t.Fatal("live payment injection escaped its identity or input boundary")
			}
			calls = append(calls, "pay-inject")
			return nil
		},
		enrollPay: func(context.Context, activationTarget, string) error {
			t.Fatal("live activation attempted database enrollment")
			return nil
		},
		configureSub2: func(context.Context, activationTarget, string) error {
			t.Fatal("live activation attempted runtime configuration or purchase enablement")
			return nil
		},
	}
	output := &bytes.Buffer{}
	code := run([]string{"--profile", "live", "--sub2-host", liveAzureSub2Host, "--pay-identity", identity}, output, lookup, unset, operations)
	if code != 0 {
		t.Fatalf("live activation result = %d %q", code, output.String())
	}
	if len(values) != 0 {
		t.Fatalf("live injected environment was not cleared: %v", values)
	}
	if strings.Join(calls, ",") != "sub2-inject,pay-inject" {
		t.Fatalf("live activation call order = %v", calls)
	}
	if strings.Contains(output.String(), requestSecret) || strings.Contains(output.String(), webhookSecret) ||
		strings.Contains(output.String(), "INSERT INTO") || strings.Contains(output.String(), "UNIFIED_PAYMENT_ENABLED") {
		t.Fatalf("live enrollment bundle exceeded its public-only boundary: %q", output.String())
	}
	var bundle liveEnrollmentBundle
	if err := json.Unmarshal(output.Bytes(), &bundle); err != nil {
		t.Fatalf("decode live enrollment bundle: %v; output=%q", err, output.String())
	}
	if bundle.SchemaVersion != liveEnrollmentSchemaVersion || bundle.State != liveEnrollmentState || bundle.Profile != "live" ||
		bundle.OrganizationID != organizationID || bundle.ProductID != productID || bundle.Environment != "live" || bundle.AppID != "app.sub2.live" ||
		bundle.PaymentBaseURL != paymentBaseURL || bundle.ReturnURL != returnURL || bundle.WebhookURL != webhookURL ||
		bundle.NextAction != "payment-binding-issue-and-proof-of-possession" {
		t.Fatalf("live enrollment bundle scope or URL mismatch: %#v", bundle)
	}
	if bundle.ProductRequestSigningKey.KeyID != liveActivationProfile.requestKeyID ||
		bundle.ProductRequestSigningKey.Algorithm != "Ed25519" || bundle.ProductRequestSigningKey.PublicKeyBase64 != requestPublic ||
		bundle.CentralWebhookSigningKey.KeyID != liveActivationProfile.webhookKeyID ||
		bundle.CentralWebhookSigningKey.Algorithm != "Ed25519" || bundle.CentralWebhookSigningKey.PublicKeyBase64 != webhookPublic {
		t.Fatalf("live enrollment public key contract mismatch: %#v", bundle)
	}
}

func TestLiveActivationRequiresExplicitAllowlistedSub2Host(t *testing.T) {
	identity := testIdentity(t)

	target, err := parseActivationTarget([]string{"--pay-identity", identity})
	if err != nil || target.profile != sandboxActivationProfile || target.sub2Host != sandboxSub2Host {
		t.Fatalf("sandbox default changed: target=%#v err=%v", target, err)
	}
	for _, args := range [][]string{
		{"--profile", "live", "--pay-identity", identity},
		{"--profile", "live", "--sub2-host", "sub2api-unapproved", "--pay-identity", identity},
		{"--profile", "live", "--sub2-host", "sub2api-new; unsafe", "--pay-identity", identity},
		{"--sub2-host", liveAzureSub2Host, "--pay-identity", identity},
	} {
		if _, err := parseActivationTarget(args); err == nil {
			t.Fatalf("unsafe activation target accepted: %q", args)
		}
	}
	for _, host := range []string{liveAzureSub2Host, liveStandbySub2Host} {
		target, err := parseActivationTarget([]string{"--profile", "live", "--sub2-host", host, "--pay-identity", identity})
		if err != nil || target.profile != liveActivationProfile || target.sub2Host != host {
			t.Fatalf("approved live target rejected: host=%q target=%#v err=%v", host, target, err)
		}
	}
}

func TestActivationRejectsMismatchedPairBeforeRemoteWork(t *testing.T) {
	values := validEnvironment(t, sandboxActivationProfile)
	_, otherPrivate, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{42}, 64)))
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	values[requestPrivateEnv] = base64.StdEncoding.EncodeToString(otherPrivate)
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	unset := func(name string) error { delete(values, name); return nil }
	called := false
	operations := recordingOperations(&called)
	output := &bytes.Buffer{}
	code := run([]string{"--pay-identity", testIdentity(t)}, output, lookup, unset, operations)
	if code != 2 || called || output.String() != "SUB2_PAYMENT_ACTIVATION_CONFIGURATION_REJECTED\n" || len(values) != 0 {
		t.Fatalf("unsafe material was not rejected: code=%d called=%v output=%q values=%v", code, called, output.String(), values)
	}
}

func TestLiveActivationRejectsSandboxMaterialBeforeRemoteWork(t *testing.T) {
	values := validEnvironment(t, sandboxActivationProfile)
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	unset := func(name string) error { delete(values, name); return nil }
	called := false
	output := &bytes.Buffer{}
	code := run([]string{"--profile", "live", "--sub2-host", liveAzureSub2Host, "--pay-identity", testIdentity(t)}, output, lookup, unset, recordingOperations(&called))
	if code != 2 || called || output.String() != "SUB2_PAYMENT_ACTIVATION_CONFIGURATION_REJECTED\n" || len(values) != 0 {
		t.Fatalf("cross-profile material was not rejected: code=%d called=%v output=%q values=%v", code, called, output.String(), values)
	}
}

func TestActivationStopsAfterFailure(t *testing.T) {
	values := validEnvironment(t, sandboxActivationProfile)
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	unset := func(name string) error { delete(values, name); return nil }
	calls := 0
	operations := activationOperations{
		injectSub2Request: func(context.Context, activationTarget, []byte) error { calls++; return io.ErrUnexpectedEOF },
		injectPayWebhook:  func(context.Context, activationTarget, []byte) error { calls++; return nil },
		enrollPay:         func(context.Context, activationTarget, string) error { calls++; return nil },
		configureSub2:     func(context.Context, activationTarget, string) error { calls++; return nil },
	}
	output := &bytes.Buffer{}
	code := run([]string{"--pay-identity", testIdentity(t)}, output, lookup, unset, operations)
	if code != 1 || calls != 1 || output.String() != "SUB2_PAYMENT_ACTIVATION_FAILED_CLOSED\n" {
		t.Fatalf("failure result = %d calls=%d output=%q", code, calls, output.String())
	}
}

func TestLiveRemoteCommandsStayScoped(t *testing.T) {
	liveSub2 := sub2RequestInjectionCommand(liveActivationProfile)
	livePay := payWebhookInjectionCommand(liveActivationProfile)
	if !strings.HasPrefix(liveSub2, "sudo -n docker exec -i sub2api-payment-vault ") ||
		!strings.Contains(liveSub2, liveRequestVaultRef) || strings.Contains(liveSub2, sandboxRequestVaultRef) ||
		!validSub2RemoteCommand(liveActivationProfile, liveSub2) || validSub2RemoteCommand(sandboxActivationProfile, liveSub2) {
		t.Fatalf("live Sub2 command lost its isolated profile boundary: %q", liveSub2)
	}
	if !strings.HasPrefix(livePay, "sudo -n docker compose --project-name totools-pay-live --file /opt/totools-pay-live/compose.live.disabled.yaml ") ||
		!strings.Contains(livePay, liveWebhookVaultRef) || strings.Contains(livePay, sandboxWebhookVaultRef) || strings.Contains(livePay, "psql") ||
		!validPayRemoteCommand(liveActivationProfile, livePay) || validPayRemoteCommand(sandboxActivationProfile, livePay) ||
		validPayRemoteCommand(liveActivationProfile, sandboxEnrollmentCommand()) {
		t.Fatalf("live payment command lost its isolated profile boundary: %q", livePay)
	}
	if !validSub2HostForProfile(liveActivationProfile, liveAzureSub2Host) || !validSub2HostForProfile(liveActivationProfile, liveStandbySub2Host) ||
		validSub2HostForProfile(liveActivationProfile, "sub2api-unapproved") || validSub2HostForProfile(sandboxActivationProfile, liveAzureSub2Host) ||
		validSub2RemoteCommand(liveActivationProfile, sandboxRuntimeConfigCommand()) {
		t.Fatal("live command or host allow-list expanded into an unsafe target")
	}
}

func TestPayRemoteUsesConfiguredAliasAndStrictHostVerification(t *testing.T) {
	arguments := payRemoteSSHArguments("/private/test-identity", "fixed remote command")
	if len(arguments) < 2 || arguments[len(arguments)-2] != payRemoteAlias || arguments[len(arguments)-1] != "fixed remote command" {
		t.Fatalf("payment SSH destination escaped the configured alias: %q", arguments)
	}
	for _, expected := range [][2]string{
		{"BatchMode=yes", "batch mode"},
		{"StrictHostKeyChecking=yes", "strict host checking"},
		{"IdentitiesOnly=yes", "identity isolation"},
		{"IdentityAgent=none", "agent isolation"},
		{"PasswordAuthentication=no", "password refusal"},
		{"KbdInteractiveAuthentication=no", "keyboard-interactive refusal"},
	} {
		if !containsSSHOption(arguments, expected[0]) {
			t.Fatalf("payment SSH arguments omitted %s: %q", expected[1], arguments)
		}
	}
	for _, forbidden := range []string{"-F", "UserKnownHostsFile=", "GlobalKnownHostsFile="} {
		if containsSSHArgument(arguments, forbidden) || containsSSHArgumentPrefix(arguments, forbidden) {
			t.Fatalf("payment SSH arguments override configured alias host-key policy: %q", arguments)
		}
	}
}

func TestGeneratedSQLAndRuntimeConfigContainNoPrivateField(t *testing.T) {
	requestPublic := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, ed25519.PublicKeySize))
	webhookPublic := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, ed25519.PublicKeySize))
	sql := enrollmentSQL(requestPublic, webhookPublic)
	configuration := runtimeConfig(webhookPublic)
	for _, value := range []string{sql, configuration} {
		if strings.Contains(value, "request_private_key_base64=") || strings.Contains(value, "webhook_private_key_base64=") {
			t.Fatal("generated public configuration contains a private value assignment")
		}
	}
	if !strings.Contains(sql, requestPublic) || !strings.Contains(sql, webhookPublic) ||
		!strings.Contains(configuration, webhookPublic) || strings.Contains(configuration, requestPublic) {
		t.Fatal("generated enrollment/config public key boundary drifted")
	}
}

func recordingOperations(called *bool) activationOperations {
	mark := func() { *called = true }
	return activationOperations{
		injectSub2Request: func(context.Context, activationTarget, []byte) error { mark(); return nil },
		injectPayWebhook:  func(context.Context, activationTarget, []byte) error { mark(); return nil },
		enrollPay:         func(context.Context, activationTarget, string) error { mark(); return nil },
		configureSub2:     func(context.Context, activationTarget, string) error { mark(); return nil },
	}
}

func containsSSHOption(arguments []string, option string) bool {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == "-o" && arguments[index+1] == option {
			return true
		}
	}
	return false
}

func containsSSHArgument(arguments []string, want string) bool {
	for _, argument := range arguments {
		if argument == want {
			return true
		}
	}
	return false
}

func containsSSHArgumentPrefix(arguments []string, prefix string) bool {
	for _, argument := range arguments {
		if strings.HasPrefix(argument, prefix) {
			return true
		}
	}
	return false
}

func validEnvironment(t *testing.T, profile activationProfile) map[string]string {
	t.Helper()
	requestPublic, requestPrivate, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{1}, 64)))
	if err != nil {
		t.Fatalf("generate request key: %v", err)
	}
	webhookPublic, webhookPrivate, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{2}, 64)))
	if err != nil {
		t.Fatalf("generate Webhook key: %v", err)
	}
	return map[string]string{
		requestPrivateEnv: base64.StdEncoding.EncodeToString(requestPrivate),
		requestPublicEnv:  base64.StdEncoding.EncodeToString(requestPublic),
		requestKeyIDEnv:   profile.requestKeyID,
		webhookPrivateEnv: base64.StdEncoding.EncodeToString(webhookPrivate),
		webhookPublicEnv:  base64.StdEncoding.EncodeToString(webhookPublic),
		webhookKeyIDEnv:   profile.webhookKeyID,
		appIDEnv:          profile.appID,
		environmentEnv:    profile.environment,
	}
}

func testIdentity(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	identity := filepath.Join(directory, "identity")
	if err := os.WriteFile(identity, []byte("test identity"), 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	return identity
}
