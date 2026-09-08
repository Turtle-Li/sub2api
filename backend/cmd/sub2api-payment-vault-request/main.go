// Command sub2api-payment-vault-request creates the JSON request for one
// integration-scoped Vault item. Its output contains two private keys and must
// only be piped directly into `infra-vault ... create-item`; never redirect it
// to a file or terminal.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"regexp"
)

const (
	vaultItemName     = "sub2api-unified-payment-sandbox-20260902"
	requestKeyID      = "sub2.request.sandbox.v1"
	webhookKeyID      = "sub2.webhook.sandbox.v1"
	liveVaultItemName = "sub2api-unified-payment-live-20260909"
	liveRequestKeyID  = "sub2.request.live.v1"
	liveWebhookKeyID  = "sub2.webhook.live.v1"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type vaultField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type vaultItemRequest struct {
	Type     string       `json:"type"`
	Name     string       `json:"name"`
	FolderID string       `json:"folderId"`
	Notes    string       `json:"notes"`
	Fields   []vaultField `json:"fields"`
}

type paymentProfile struct {
	name         string
	vaultItem    string
	requestKeyID string
	webhookKeyID string
	appID        string
	environment  string
}

var sandboxPaymentProfile = paymentProfile{
	name:         "sandbox",
	vaultItem:    vaultItemName,
	requestKeyID: requestKeyID,
	webhookKeyID: webhookKeyID,
	appID:        "app.sub2.sandbox",
	environment:  "sandbox",
}

var livePaymentProfile = paymentProfile{
	name:         "live",
	vaultItem:    liveVaultItemName,
	requestKeyID: liveRequestKeyID,
	webhookKeyID: liveWebhookKeyID,
	appID:        "app.sub2.live",
	environment:  "live",
}

func main() {
	folderID, profile, err := parseVaultItemRequestArgs(os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	request, err := newVaultItemRequestForProfile(folderID, profile, rand.Reader)
	if err != nil || json.NewEncoder(os.Stdout).Encode(request) != nil {
		os.Exit(1)
	}
}

func parseVaultItemRequestArgs(args []string) (string, paymentProfile, error) {
	flags := flag.NewFlagSet("sub2api-payment-vault-request", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	folderID := flags.String("folder-id", "", "Infrastructure folder UUID")
	profileName := flags.String("profile", "sandbox", "Payment profile: sandbox or live")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !uuidPattern.MatchString(*folderID) {
		return "", paymentProfile{}, errors.New("invalid payment Vault request arguments")
	}
	profile, err := paymentProfileFor(*profileName)
	if err != nil {
		return "", paymentProfile{}, err
	}
	return *folderID, profile, nil
}

func paymentProfileFor(name string) (paymentProfile, error) {
	switch name {
	case "", "sandbox":
		return sandboxPaymentProfile, nil
	case "live":
		return livePaymentProfile, nil
	default:
		return paymentProfile{}, errors.New("unsupported payment profile")
	}
}

func newVaultItemRequest(folderID string, random io.Reader) (vaultItemRequest, error) {
	return newVaultItemRequestForProfile(folderID, sandboxPaymentProfile, random)
}

func newVaultItemRequestForProfile(folderID string, profile paymentProfile, random io.Reader) (vaultItemRequest, error) {
	if !uuidPattern.MatchString(folderID) || random == nil || profile != sandboxPaymentProfile && profile != livePaymentProfile {
		return vaultItemRequest{}, errors.New("invalid payment Vault request configuration")
	}
	requestPublic, requestPrivate, err := ed25519.GenerateKey(random)
	if err != nil {
		return vaultItemRequest{}, errors.New("request key generation failed")
	}
	defer clear(requestPrivate)
	webhookPublic, webhookPrivate, err := ed25519.GenerateKey(random)
	if err != nil {
		return vaultItemRequest{}, errors.New("webhook key generation failed")
	}
	defer clear(webhookPrivate)
	if string(requestPublic) == string(webhookPublic) {
		return vaultItemRequest{}, errors.New("generated payment keys are not independent")
	}
	return vaultItemRequest{
		Type:     "secure-note",
		Name:     profile.vaultItem,
		FolderID: folderID,
		Notes: "Sub2 / totools-pay " + profile.name + " integration keys. Private fields may only be consumed through " +
			"the SHA-256-pinned memory-agent injection workflow; never copy them to Git, Docker metadata, files, logs, or chat.",
		Fields: []vaultField{
			{Name: "request_private_key_base64", Value: base64.StdEncoding.EncodeToString(requestPrivate), Type: "hidden"},
			{Name: "request_public_key_base64", Value: base64.StdEncoding.EncodeToString(requestPublic), Type: "text"},
			{Name: "request_key_id", Value: profile.requestKeyID, Type: "text"},
			{Name: "webhook_private_key_base64", Value: base64.StdEncoding.EncodeToString(webhookPrivate), Type: "hidden"},
			{Name: "webhook_public_key_base64", Value: base64.StdEncoding.EncodeToString(webhookPublic), Type: "text"},
			{Name: "webhook_key_id", Value: profile.webhookKeyID, Type: "text"},
			{Name: "app_id", Value: profile.appID, Type: "text"},
			{Name: "environment", Value: profile.environment, Type: "text"},
		},
	}, nil
}
