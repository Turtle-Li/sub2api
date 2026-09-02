package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testCredentialRef = "vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64"

func TestMemoryVaultLoadsOnlyAllowedFieldAndServesKVv2(t *testing.T) {
	vault, err := newMemoryVault([]string{testCredentialRef})
	if err != nil {
		t.Fatalf("newMemoryVault: %v", err)
	}
	defer vault.clear()

	request := httptest.NewRequest(http.MethodGet, "http://vault/v1/secret/data/sub2api/unified-payment/sandbox", nil)
	unloaded := httptest.NewRecorder()
	vault.readHandler().ServeHTTP(unloaded, request)
	if unloaded.Code != http.StatusServiceUnavailable {
		t.Fatalf("unloaded status = %d, want 503", unloaded.Code)
	}

	secret := []byte("private-key-base64")
	if err := vault.load(reference{path: "secret/data/sub2api/unified-payment/sandbox", field: "request_private_key_base64"}, secret); err != nil {
		t.Fatalf("load: %v", err)
	}
	secret[0] = 'x'
	loaded := httptest.NewRecorder()
	vault.readHandler().ServeHTTP(loaded, request)
	if loaded.Code != http.StatusOK {
		t.Fatalf("loaded status = %d, want 200", loaded.Code)
	}
	var envelope struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(loaded.Body.Bytes(), &envelope); err != nil || envelope.Data.Data["request_private_key_base64"] != "private-key-base64" {
		t.Fatal("agent did not serve the independent in-memory value")
	}
	if !vault.ready() {
		t.Fatal("agent should be ready after its only allowed field is loaded")
	}
}

func TestAdminHandlerRejectsUnknownOversizedAndWrongHost(t *testing.T) {
	vault, err := newMemoryVault([]string{testCredentialRef})
	if err != nil {
		t.Fatalf("newMemoryVault: %v", err)
	}

	for _, request := range []*http.Request{
		newAdminRequest("http://unix/v1/load/secret/data/sub2api/other?field=request_private_key_base64", "value"),
		newAdminRequest("http://wrong/v1/load/secret/data/sub2api/unified-payment/sandbox?field=request_private_key_base64", "value"),
		newAdminRequest("http://unix/v1/load/secret/data/sub2api/unified-payment/sandbox?field=request_private_key_base64", strings.Repeat("x", maxInjectedSecretBytes+1)),
	} {
		recorder := httptest.NewRecorder()
		vault.adminHandler().ServeHTTP(recorder, request)
		if recorder.Code < 400 {
			t.Fatalf("unsafe load status = %d", recorder.Code)
		}
	}
	if vault.ready() {
		t.Fatal("rejected loads must not make the agent ready")
	}
}

func newAdminRequest(target, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/octet-stream")
	return request
}

func TestLoadOverPrivateUnixSocket(t *testing.T) {
	vault, err := newMemoryVault([]string{testCredentialRef})
	if err != nil {
		t.Fatalf("newMemoryVault: %v", err)
	}
	directory := privateTempDir(t)
	socket := filepath.Join(directory, "admin.sock")
	listener, err := listenPrivateUnix(socket)
	if err != nil {
		t.Fatalf("listenPrivateUnix: %v", err)
	}
	server := hardenedServer(vault.adminHandler())
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
		removeOwnedSocket(socket)
		<-done
	}()

	ref, err := parseReference(testCredentialRef)
	if err != nil {
		t.Fatalf("parseReference: %v", err)
	}
	if err := loadOverUnix(context.Background(), socket, ref, []byte("bounded-secret")); err != nil {
		t.Fatalf("loadOverUnix: %v", err)
	}
	payload, allowed, ready := vault.payload(ref.path)
	defer zero(payload)
	if !allowed || !ready || !bytes.Contains(payload, []byte("bounded-secret")) {
		t.Fatal("private socket load did not populate the allowed field")
	}
}

func TestPublicUnixSocketReadiness(t *testing.T) {
	vault, err := newMemoryVault([]string{testCredentialRef})
	if err != nil {
		t.Fatalf("newMemoryVault: %v", err)
	}
	directory := privateTempDir(t)
	socket := filepath.Join(directory, "public.sock")
	listener, err := listenPrivateUnix(socket)
	if err != nil {
		t.Fatalf("listenPrivateUnix: %v", err)
	}
	server := hardenedServer(vault.readHandler())
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
		removeOwnedSocket(socket)
		<-done
	}()

	if err := checkReady(context.Background(), socket); err == nil {
		t.Fatal("unloaded agent unexpectedly reported ready")
	}
	ref, _ := parseReference(testCredentialRef)
	if err := vault.load(ref, []byte("value")); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := checkReady(context.Background(), socket); err != nil {
		t.Fatalf("ready agent rejected: %v", err)
	}
}

func TestReadHandlerRejectsCredentialHeadersAndUnknownPaths(t *testing.T) {
	vault, err := newMemoryVault([]string{testCredentialRef})
	if err != nil {
		t.Fatalf("newMemoryVault: %v", err)
	}
	ref, _ := parseReference(testCredentialRef)
	if err := vault.load(ref, []byte("secret")); err != nil {
		t.Fatalf("load: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://vault/v1/secret/data/sub2api/unified-payment/sandbox", nil)
	request.Header.Set("X-Vault-Token", "forbidden")
	recorder := httptest.NewRecorder()
	vault.readHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Body.String(), "secret") {
		t.Fatal("authenticated-looking request was not rejected safely")
	}

	unknown := httptest.NewRequest(http.MethodGet, "http://vault/v1/secret/data/sub2api/unknown", nil)
	unknownRecorder := httptest.NewRecorder()
	vault.readHandler().ServeHTTP(unknownRecorder, unknown)
	if unknownRecorder.Code != http.StatusNotFound {
		t.Fatalf("unknown path status = %d, want 404", unknownRecorder.Code)
	}
}

func TestReferenceSocketAndSecretValidation(t *testing.T) {
	for _, raw := range []string{
		"", "http://secret/data/a#field", "vault:///secret/data/a#field",
		"vault://secret/../a#field", "vault://secret/data/a#field-name", "vault://secret/data/a#field#again",
	} {
		if _, err := parseReference(raw); err == nil {
			t.Fatalf("parseReference(%q) unexpectedly succeeded", raw)
		}
	}
	for _, path := range []string{"", "relative.sock", "/tmp/not-socket", "/tmp/a/../admin.sock"} {
		if validateSocketPath(path) == nil {
			t.Fatalf("validateSocketPath(%q) unexpectedly succeeded", path)
		}
	}
	for _, input := range [][]byte{nil, {}, {'a', 0, 'b'}, bytes.Repeat([]byte{'x'}, maxInjectedSecretBytes+1)} {
		if value, err := readBoundedSecret(bytes.NewReader(input)); err == nil || value != nil {
			t.Fatalf("readBoundedSecret(%q) unexpectedly succeeded", input)
		}
	}
	if value, err := readBoundedSecret(io.NopCloser(strings.NewReader("ok"))); err != nil || string(value) != "ok" {
		t.Fatalf("valid secret rejected: %q, %v", value, err)
	}
}

func TestRunLoadNeverEchoesInjectedValue(t *testing.T) {
	output := &bytes.Buffer{}
	exitCode := run([]string{"load", "--admin-socket", "/missing/private/admin.sock", "--ref", testCredentialRef},
		strings.NewReader("do-not-echo"), output)
	if exitCode != 1 {
		t.Fatalf("run load exit code = %d, want 1", exitCode)
	}
	if strings.Contains(output.String(), "do-not-echo") {
		t.Fatal("load failure echoed injected value")
	}
}

func TestPrivateSocketPermissions(t *testing.T) {
	directory := privateTempDir(t)
	socket := filepath.Join(directory, "admin.sock")
	listener, err := listenPrivateUnix(socket)
	if err != nil {
		t.Fatalf("listenPrivateUnix: %v", err)
	}
	defer listener.Close()
	detail, err := os.Stat(socket)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if detail.Mode().Perm() != 0o600 || detail.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket mode = %v, want srw-------", detail.Mode())
	}

	worldDirectory := t.TempDir()
	if err := os.Chmod(worldDirectory, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := listenPrivateUnix(filepath.Join(worldDirectory, "admin.sock")); err == nil {
		t.Fatal("world-accessible socket directory was accepted")
	}
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "s2v-")
	if err != nil {
		t.Fatalf("create temp directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("chmod temp directory: %v", err)
	}
	return directory
}
