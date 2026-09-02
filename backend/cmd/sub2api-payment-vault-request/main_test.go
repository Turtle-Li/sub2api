package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"testing"
)

const testFolderID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func TestNewVaultItemRequestCreatesIndependentValidatedPairs(t *testing.T) {
	entropy := make([]byte, 64)
	for index := range entropy {
		entropy[index] = byte(index + 1)
	}
	request, err := newVaultItemRequest(testFolderID, bytes.NewReader(entropy))
	if err != nil {
		t.Fatalf("newVaultItemRequest: %v", err)
	}
	if request.Type != "secure-note" || request.Name != vaultItemName || request.FolderID != testFolderID || len(request.Fields) != 8 {
		t.Fatalf("unexpected Vault request metadata: %#v", request)
	}
	fields := make(map[string]string, len(request.Fields))
	for _, field := range request.Fields {
		fields[field.Name] = field.Value
	}
	assertPair(t, fields["request_private_key_base64"], fields["request_public_key_base64"])
	assertPair(t, fields["webhook_private_key_base64"], fields["webhook_public_key_base64"])
	if fields["request_private_key_base64"] == fields["webhook_private_key_base64"] ||
		fields["request_key_id"] != requestKeyID || fields["webhook_key_id"] != webhookKeyID ||
		fields["app_id"] != "app.sub2.sandbox" || fields["environment"] != "sandbox" {
		t.Fatal("generated integration identity is inconsistent")
	}
}

func TestNewVaultItemRequestFailsClosed(t *testing.T) {
	if _, err := newVaultItemRequest("bad-folder", bytes.NewReader(make([]byte, 64))); err == nil {
		t.Fatal("invalid folder accepted")
	}
	if _, err := newVaultItemRequest(testFolderID, bytes.NewReader(make([]byte, 31))); err == nil {
		t.Fatal("short entropy source accepted")
	}
	if _, err := newVaultItemRequest(testFolderID, bytes.NewReader(make([]byte, 64))); err == nil {
		t.Fatal("repeated request and Webhook key accepted")
	}
}

func assertPair(t *testing.T, encodedPrivate, encodedPublic string) {
	t.Helper()
	privateKey, err := base64.StdEncoding.Strict().DecodeString(encodedPrivate)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		t.Fatal("invalid private key field")
	}
	publicKey, err := base64.StdEncoding.Strict().DecodeString(encodedPublic)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		t.Fatal("invalid public key field")
	}
	if !bytes.Equal(privateKey[ed25519.SeedSize:], publicKey) {
		t.Fatal("public key does not match private key")
	}
}
