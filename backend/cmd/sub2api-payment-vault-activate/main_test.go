package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActivationValidatesClearsAndRunsBoundedOperations(t *testing.T) {
	values := validEnvironment(t)
	requestSecret := values[requestPrivateEnv]
	webhookSecret := values[webhookPrivateEnv]
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	unset := func(name string) error { delete(values, name); return nil }
	identity := testIdentity(t)
	var calls []string
	operations := activationOperations{
		injectSub2Request: func(_ context.Context, input []byte) error {
			if string(input) != requestSecret {
				t.Fatal("Sub2 injection did not receive the validated Base64 private key")
			}
			calls = append(calls, "sub2-inject")
			return nil
		},
		injectPayWebhook: func(_ context.Context, gotIdentity string, input []byte) error {
			if gotIdentity != identity || string(input) != webhookSecret {
				t.Fatal("payment injection input mismatch")
			}
			calls = append(calls, "pay-inject")
			return nil
		},
		enrollPay: func(_ context.Context, gotIdentity, sql string) error {
			if gotIdentity != identity || !strings.Contains(sql, "app.sub2.sandbox") ||
				strings.Contains(sql, requestSecret) || strings.Contains(sql, webhookSecret) {
				t.Fatal("enrollment SQL was not public-only or scope-bound")
			}
			calls = append(calls, "pay-enroll")
			return nil
		},
		configureSub2: func(_ context.Context, configuration string) error {
			if !strings.Contains(configuration, "UNIFIED_PAYMENT_ENABLED=true") ||
				!strings.Contains(configuration, "https://www.turtleligpt.com/payment/result") ||
				strings.Contains(configuration, requestSecret) || strings.Contains(configuration, webhookSecret) {
				t.Fatal("Sub2 runtime config was not public-only or scope-bound")
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

func TestActivationRejectsMismatchedPairBeforeRemoteWork(t *testing.T) {
	values := validEnvironment(t)
	_, otherPrivate, err := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{42}, 64)))
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	values[requestPrivateEnv] = base64.StdEncoding.EncodeToString(otherPrivate)
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	unset := func(name string) error { delete(values, name); return nil }
	called := false
	operations := activationOperations{
		injectSub2Request: func(context.Context, []byte) error { called = true; return nil },
		injectPayWebhook:  func(context.Context, string, []byte) error { called = true; return nil },
		enrollPay:         func(context.Context, string, string) error { called = true; return nil },
		configureSub2:     func(context.Context, string) error { called = true; return nil },
	}
	output := &bytes.Buffer{}
	code := run([]string{"--pay-identity", testIdentity(t)}, output, lookup, unset, operations)
	if code != 2 || called || output.String() != "SUB2_PAYMENT_ACTIVATION_CONFIGURATION_REJECTED\n" || len(values) != 0 {
		t.Fatalf("unsafe material was not rejected: code=%d called=%v output=%q values=%v", code, called, output.String(), values)
	}
}

func TestActivationStopsAfterFailure(t *testing.T) {
	values := validEnvironment(t)
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	unset := func(name string) error { delete(values, name); return nil }
	calls := 0
	operations := activationOperations{
		injectSub2Request: func(context.Context, []byte) error { calls++; return io.ErrUnexpectedEOF },
		injectPayWebhook:  func(context.Context, string, []byte) error { calls++; return nil },
		enrollPay:         func(context.Context, string, string) error { calls++; return nil },
		configureSub2:     func(context.Context, string) error { calls++; return nil },
	}
	output := &bytes.Buffer{}
	code := run([]string{"--pay-identity", testIdentity(t)}, output, lookup, unset, operations)
	if code != 1 || calls != 1 || output.String() != "SUB2_PAYMENT_ACTIVATION_FAILED_CLOSED\n" {
		t.Fatalf("failure result = %d calls=%d output=%q", code, calls, output.String())
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

func validEnvironment(t *testing.T) map[string]string {
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
		requestKeyIDEnv:   requestKeyID,
		webhookPrivateEnv: base64.StdEncoding.EncodeToString(webhookPrivate),
		webhookPublicEnv:  base64.StdEncoding.EncodeToString(webhookPublic),
		webhookKeyIDEnv:   webhookKeyID,
		appIDEnv:          appID,
		environmentEnv:    "sandbox",
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
