// Command sub2api-payment-vault-activate is the only supported bridge from a
// hash-pinned infra-vault process into the Sub2 and totools-pay sandbox
// runtimes. It validates both keypairs, clears injected environment values
// before network access, and sends private values only on SSH standard input.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	requestPrivateEnv = "SUB2_PAYMENT_REQUEST_PRIVATE_KEY_BASE64"
	requestPublicEnv  = "SUB2_PAYMENT_REQUEST_PUBLIC_KEY_BASE64"
	requestKeyIDEnv   = "SUB2_PAYMENT_REQUEST_KEY_ID"
	webhookPrivateEnv = "SUB2_PAYMENT_WEBHOOK_PRIVATE_KEY_BASE64"
	webhookPublicEnv  = "SUB2_PAYMENT_WEBHOOK_PUBLIC_KEY_BASE64"
	webhookKeyIDEnv   = "SUB2_PAYMENT_WEBHOOK_KEY_ID"
	appIDEnv          = "SUB2_PAYMENT_APP_ID"
	environmentEnv    = "SUB2_PAYMENT_ENVIRONMENT"

	requestKeyID = "sub2.request.sandbox.v1"
	webhookKeyID = "sub2.webhook.sandbox.v1"
	appID        = "app.sub2.sandbox"

	requestVaultRef = "vault://secret/data/sub2api/unified-payment/sandbox#request_private_key_base64"
	webhookVaultRef = "vault://secret/data/sub2api/unified-payment/sandbox#webhook_private_key_base64"

	payRemoteHost        = "111.231.164.29"
	payRemoteUser        = "ubuntu"
	payRemoteHostKeyLine = "111.231.164.29 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILbqLNiydqNXBr616PGsyJ1JrPkqLCcwy0VARxOqPeY9"

	activationTimeout = 2 * time.Minute
)

type environmentLookup func(string) (string, bool)
type environmentUnset func(string) error

type activationOperations struct {
	injectSub2Request func(context.Context, []byte) error
	injectPayWebhook  func(context.Context, string, []byte) error
	enrollPay         func(context.Context, string, string) error
	configureSub2     func(context.Context, string) error
}

type activationMaterial struct {
	requestPrivate []byte
	requestPublic  string
	webhookPrivate []byte
	webhookPublic  string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.LookupEnv, os.Unsetenv, productionOperations()))
}

func run(args []string, output io.Writer, lookup environmentLookup, unset environmentUnset, operations activationOperations) int {
	flags := flag.NewFlagSet("sub2api-payment-vault-activate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	payIdentity := flags.String("pay-identity", "", "totools-pay project SSH identity")
	if flags.Parse(args) != nil || flags.NArg() != 0 || lookup == nil || unset == nil ||
		validatePrivateIdentity(*payIdentity) != nil || !validOperations(operations) {
		return classification(output, "SUB2_PAYMENT_ACTIVATION_CONFIGURATION_REJECTED", 2)
	}

	material, ok := loadMaterial(lookup, unset)
	if !ok {
		material.clear()
		return classification(output, "SUB2_PAYMENT_ACTIVATION_CONFIGURATION_REJECTED", 2)
	}
	defer material.clear()
	ctx, cancel := context.WithTimeout(context.Background(), activationTimeout)
	defer cancel()

	if err := operations.injectSub2Request(ctx, material.requestPrivate); err != nil {
		return classification(output, "SUB2_PAYMENT_ACTIVATION_FAILED_CLOSED", 1)
	}
	if err := operations.injectPayWebhook(ctx, *payIdentity, material.webhookPrivate); err != nil {
		return classification(output, "SUB2_PAYMENT_ACTIVATION_FAILED_CLOSED", 1)
	}
	if err := operations.enrollPay(ctx, *payIdentity, enrollmentSQL(material.requestPublic, material.webhookPublic)); err != nil {
		return classification(output, "SUB2_PAYMENT_ACTIVATION_FAILED_CLOSED", 1)
	}
	if err := operations.configureSub2(ctx, runtimeConfig(material.webhookPublic)); err != nil {
		return classification(output, "SUB2_PAYMENT_ACTIVATION_FAILED_CLOSED", 1)
	}
	return classification(output, "SUB2_PAYMENT_SANDBOX_ACTIVATED", 0)
}

func validOperations(operations activationOperations) bool {
	return operations.injectSub2Request != nil && operations.injectPayWebhook != nil &&
		operations.enrollPay != nil && operations.configureSub2 != nil
}

func loadMaterial(lookup environmentLookup, unset environmentUnset) (activationMaterial, bool) {
	names := []string{
		requestPrivateEnv, requestPublicEnv, requestKeyIDEnv, webhookPrivateEnv,
		webhookPublicEnv, webhookKeyIDEnv, appIDEnv, environmentEnv,
	}
	values := make(map[string][]byte, len(names))
	valid := true
	for _, name := range names {
		value, ok := lookup(name)
		if !ok || value == "" || len(value) > 512 || strings.ContainsRune(value, '\x00') {
			valid = false
		} else {
			values[name] = []byte(value)
		}
	}
	for _, name := range names {
		if unset(name) != nil {
			valid = false
		}
	}
	if !valid || string(values[requestKeyIDEnv]) != requestKeyID || string(values[webhookKeyIDEnv]) != webhookKeyID ||
		string(values[appIDEnv]) != appID || string(values[environmentEnv]) != "sandbox" {
		clearMap(values)
		return activationMaterial{}, false
	}
	requestPrivate, requestPublic, ok := decodePair(values[requestPrivateEnv], values[requestPublicEnv])
	if !ok {
		clearMap(values)
		return activationMaterial{}, false
	}
	webhookPrivate, webhookPublic, ok := decodePair(values[webhookPrivateEnv], values[webhookPublicEnv])
	if !ok || subtle.ConstantTimeCompare(requestPrivate, webhookPrivate) == 1 {
		zero(requestPrivate)
		zero(webhookPrivate)
		clearMap(values)
		return activationMaterial{}, false
	}
	requestPrivateEncoded := []byte(base64.StdEncoding.EncodeToString(requestPrivate))
	webhookPrivateEncoded := []byte(base64.StdEncoding.EncodeToString(webhookPrivate))
	requestPublicEncoded := base64.StdEncoding.EncodeToString(requestPublic)
	webhookPublicEncoded := base64.StdEncoding.EncodeToString(webhookPublic)
	zero(requestPrivate)
	zero(requestPublic)
	zero(webhookPrivate)
	zero(webhookPublic)
	clearMap(values)
	return activationMaterial{
		requestPrivate: requestPrivateEncoded,
		requestPublic:  requestPublicEncoded,
		webhookPrivate: webhookPrivateEncoded,
		webhookPublic:  webhookPublicEncoded,
	}, true
}

func decodePair(encodedPrivate, encodedPublic []byte) ([]byte, []byte, bool) {
	privateKey, privateErr := base64.StdEncoding.Strict().DecodeString(string(encodedPrivate))
	publicKey, publicErr := base64.StdEncoding.Strict().DecodeString(string(encodedPublic))
	if privateErr != nil || publicErr != nil || len(privateKey) != ed25519.PrivateKeySize || len(publicKey) != ed25519.PublicKeySize {
		zero(privateKey)
		zero(publicKey)
		return nil, nil, false
	}
	derived := ed25519.NewKeyFromSeed(privateKey[:ed25519.SeedSize])
	ok := subtle.ConstantTimeCompare(privateKey, derived) == 1 && subtle.ConstantTimeCompare(publicKey, derived[ed25519.SeedSize:]) == 1
	zero(derived)
	if !ok {
		zero(privateKey)
		zero(publicKey)
		return nil, nil, false
	}
	return privateKey, publicKey, true
}

func (material *activationMaterial) clear() {
	if material == nil {
		return
	}
	zero(material.requestPrivate)
	zero(material.webhookPrivate)
	*material = activationMaterial{}
}

func productionOperations() activationOperations {
	return activationOperations{
		injectSub2Request: func(ctx context.Context, secret []byte) error {
			return runSub2Remote(ctx,
				"docker exec -i sub2api-payment-vault /app/sub2api-vault-agent load --admin-socket /run/sub2api-payment-vault-admin/admin.sock --ref "+requestVaultRef,
				secret)
		},
		injectPayWebhook: func(ctx context.Context, identity string, secret []byte) error {
			return runPayRemote(ctx, identity,
				"cd /opt/totools-pay && docker compose --project-name totools-pay --file compose.sandbox.yaml exec -T payment-vault-worker /app/payment-vault-agent load --admin-socket /run/payment-vault-agent/admin.sock --ref "+webhookVaultRef,
				secret)
		},
		enrollPay: func(ctx context.Context, identity, sql string) error {
			return runPayRemote(ctx, identity,
				"cd /opt/totools-pay && docker compose --project-name totools-pay --file compose.sandbox.yaml exec -T --user postgres payment-postgres psql --no-psqlrc --set ON_ERROR_STOP=1 --dbname payment",
				[]byte(sql))
		},
		configureSub2: func(ctx context.Context, configuration string) error {
			return runSub2Remote(ctx, "/opt/sub2api/scripts/sub2api-unified-payment-config.sh", []byte(configuration))
		},
	}
}

func runSub2Remote(ctx context.Context, remoteCommand string, input []byte) error {
	if ctx == nil || !validSub2RemoteCommand(remoteCommand) || len(input) == 0 || len(input) > 32*1024 {
		return errors.New("Sub2 remote operation rejected")
	}
	arguments := []string{
		"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none",
		"-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no",
		"-o", "StrictHostKeyChecking=yes", "-o", "VerifyHostKeyDNS=no", "-o", "CheckHostIP=yes",
		"-o", "ConnectTimeout=10", "-o", "ConnectionAttempts=1", "-o", "LogLevel=ERROR",
		"sub2api-new", remoteCommand,
	}
	return runSSH(ctx, arguments, input)
}

func validSub2RemoteCommand(command string) bool {
	return command == "/opt/sub2api/scripts/sub2api-unified-payment-config.sh" ||
		command == "docker exec -i sub2api-payment-vault /app/sub2api-vault-agent load --admin-socket /run/sub2api-payment-vault-admin/admin.sock --ref "+requestVaultRef
}

func runPayRemote(ctx context.Context, identity, remoteCommand string, input []byte) error {
	if ctx == nil || validatePrivateIdentity(identity) != nil || !validPayRemoteCommand(remoteCommand) || len(input) == 0 || len(input) > 64*1024 {
		return errors.New("payment remote operation rejected")
	}
	knownHosts, err := os.CreateTemp("", "totools-pay-known-hosts-*.tmp")
	if err != nil {
		return errors.New("payment remote operation unavailable")
	}
	knownHostsPath := knownHosts.Name()
	defer func() {
		_ = knownHosts.Close()
		_ = os.Remove(knownHostsPath)
	}()
	if err := knownHosts.Chmod(0o600); err != nil {
		return errors.New("payment remote operation unavailable")
	}
	if _, err := io.WriteString(knownHosts, payRemoteHostKeyLine+"\n"); err != nil || knownHosts.Sync() != nil || knownHosts.Close() != nil {
		return errors.New("payment remote operation unavailable")
	}
	arguments := []string{
		"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none",
		"-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no",
		"-o", "StrictHostKeyChecking=yes", "-o", "VerifyHostKeyDNS=no", "-o", "CheckHostIP=yes",
		"-o", "HostKeyAlgorithms=ssh-ed25519", "-o", "PubkeyAcceptedAlgorithms=ssh-ed25519",
		"-o", "UserKnownHostsFile=" + knownHostsPath, "-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=10", "-o", "ConnectionAttempts=1", "-o", "LogLevel=ERROR",
		"-i", identity, payRemoteUser + "@" + payRemoteHost, remoteCommand,
	}
	return runSSH(ctx, arguments, input)
}

func validPayRemoteCommand(command string) bool {
	return command == "cd /opt/totools-pay && docker compose --project-name totools-pay --file compose.sandbox.yaml exec -T payment-vault-worker /app/payment-vault-agent load --admin-socket /run/payment-vault-agent/admin.sock --ref "+webhookVaultRef ||
		command == "cd /opt/totools-pay && docker compose --project-name totools-pay --file compose.sandbox.yaml exec -T --user postgres payment-postgres psql --no-psqlrc --set ON_ERROR_STOP=1 --dbname payment"
}

func runSSH(ctx context.Context, arguments []string, input []byte) error {
	command := exec.CommandContext(ctx, "/usr/bin/ssh", arguments...)
	command.Env = []string{"HOME=" + os.Getenv("HOME"), "LANG=C", "LC_ALL=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	command.Stdin = bytes.NewReader(input)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return errors.New("remote operation failed")
	}
	return nil
}

func validatePrivateIdentity(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, '\x00') {
		return errors.New("invalid SSH identity")
	}
	detail, err := os.Lstat(path)
	if err != nil || !detail.Mode().IsRegular() || detail.Mode()&os.ModeSymlink != 0 || detail.Mode().Perm()&0o077 != 0 {
		return errors.New("invalid SSH identity")
	}
	owner, ok := detail.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Getuid() {
		return errors.New("invalid SSH identity")
	}
	return nil
}

func enrollmentSQL(requestPublic, webhookPublic string) string {
	return fmt.Sprintf(`BEGIN;
SELECT pg_advisory_xact_lock(hashtext('totools-pay-sub2-sandbox-enrollment'));

INSERT INTO payment.application_signing_keys
  (organization_id, product_id, environment, app_id, key_id, algorithm, public_key, status)
VALUES
  ('84fc3e66-e959-4bc8-8d78-6f8c3d3483fb', '00da03c5-bc5c-4edb-9d4c-c77da0e969d5', 'sandbox',
   'app.sub2.sandbox', 'sub2.request.sandbox.v1', 'Ed25519', decode('%s', 'base64'), 'ACTIVE')
ON CONFLICT (app_id, key_id) DO NOTHING;

INSERT INTO payment.allowed_return_urls
  (id, organization_id, product_id, environment, app_id, return_url, status)
VALUES
  ('7b900ef8-26eb-4eaa-833f-876db5de13d1', '84fc3e66-e959-4bc8-8d78-6f8c3d3483fb',
   '00da03c5-bc5c-4edb-9d4c-c77da0e969d5', 'sandbox', 'app.sub2.sandbox',
   'https://www.turtleligpt.com/payment/result', 'ACTIVE')
ON CONFLICT (app_id, return_url) DO NOTHING;

INSERT INTO payment.webhook_signing_keys
  (organization_id, product_id, environment, app_id, key_id, algorithm, public_key,
   private_key_vault_ref, status)
VALUES
  ('84fc3e66-e959-4bc8-8d78-6f8c3d3483fb', '00da03c5-bc5c-4edb-9d4c-c77da0e969d5', 'sandbox',
   'app.sub2.sandbox', 'sub2.webhook.sandbox.v1', 'Ed25519', decode('%s', 'base64'),
   'vault://secret/data/sub2api/unified-payment/sandbox#webhook_private_key_base64', 'ACTIVE')
ON CONFLICT (app_id, key_id) DO NOTHING;

INSERT INTO payment.webhook_endpoints
  (id, organization_id, product_id, environment, app_id, endpoint_url, signing_key_id, status, max_attempts)
VALUES
  ('f8eff044-cdaf-4a6e-8ef6-a3ea19206283', '84fc3e66-e959-4bc8-8d78-6f8c3d3483fb',
   '00da03c5-bc5c-4edb-9d4c-c77da0e969d5', 'sandbox', 'app.sub2.sandbox',
   'https://api.turtleligpt.com/api/v1/payment/webhook/unified', 'sub2.webhook.sandbox.v1', 'ACTIVE', 12)
ON CONFLICT (id) DO NOTHING;

DO $verify$
BEGIN
  IF (SELECT count(*) FROM payment.application_signing_keys
       WHERE app_id='app.sub2.sandbox' AND key_id='sub2.request.sandbox.v1'
         AND organization_id='84fc3e66-e959-4bc8-8d78-6f8c3d3483fb'
         AND product_id='00da03c5-bc5c-4edb-9d4c-c77da0e969d5' AND environment='sandbox'
         AND algorithm='Ed25519' AND public_key=decode('%s', 'base64') AND status='ACTIVE') <> 1
     OR (SELECT count(*) FROM payment.allowed_return_urls
         WHERE id='7b900ef8-26eb-4eaa-833f-876db5de13d1'
           AND organization_id='84fc3e66-e959-4bc8-8d78-6f8c3d3483fb'
           AND product_id='00da03c5-bc5c-4edb-9d4c-c77da0e969d5'
           AND environment='sandbox' AND app_id='app.sub2.sandbox'
           AND return_url='https://www.turtleligpt.com/payment/result' AND status='ACTIVE') <> 1
     OR (SELECT count(*) FROM payment.webhook_signing_keys
         WHERE app_id='app.sub2.sandbox' AND key_id='sub2.webhook.sandbox.v1'
           AND organization_id='84fc3e66-e959-4bc8-8d78-6f8c3d3483fb'
           AND product_id='00da03c5-bc5c-4edb-9d4c-c77da0e969d5'
           AND environment='sandbox' AND algorithm='Ed25519'
           AND public_key=decode('%s', 'base64')
           AND private_key_vault_ref='vault://secret/data/sub2api/unified-payment/sandbox#webhook_private_key_base64'
           AND status='ACTIVE') <> 1
     OR (SELECT count(*) FROM payment.webhook_endpoints
         WHERE id='f8eff044-cdaf-4a6e-8ef6-a3ea19206283'
           AND organization_id='84fc3e66-e959-4bc8-8d78-6f8c3d3483fb'
           AND product_id='00da03c5-bc5c-4edb-9d4c-c77da0e969d5'
           AND environment='sandbox' AND app_id='app.sub2.sandbox'
           AND endpoint_url='https://api.turtleligpt.com/api/v1/payment/webhook/unified'
           AND signing_key_id='sub2.webhook.sandbox.v1' AND status='ACTIVE' AND max_attempts=12) <> 1
  THEN
    RAISE EXCEPTION 'Sub2 sandbox enrollment verification failed';
  END IF;
END
$verify$;
COMMIT;
`, requestPublic, webhookPublic, requestPublic, webhookPublic)
}

func runtimeConfig(webhookPublic string) string {
	return strings.Join([]string{
		"SUB2API_UNIFIED_PAYMENT_VAULT_VOLUME=sub2api_unified_payment_vault",
		"UNIFIED_PAYMENT_ENABLED=true",
		"UNIFIED_PAYMENT_BASE_URL=https://pay.totools.cn",
		"UNIFIED_PAYMENT_ENVIRONMENT=sandbox",
		"UNIFIED_PAYMENT_ORGANIZATION_ID=84fc3e66-e959-4bc8-8d78-6f8c3d3483fb",
		"UNIFIED_PAYMENT_PRODUCT_ID=00da03c5-bc5c-4edb-9d4c-c77da0e969d5",
		"UNIFIED_PAYMENT_APP_ID=app.sub2.sandbox",
		"UNIFIED_PAYMENT_REQUEST_KEY_ID=sub2.request.sandbox.v1",
		"UNIFIED_PAYMENT_REQUEST_PRIVATE_KEY_VAULT_REF=" + requestVaultRef,
		"UNIFIED_PAYMENT_VAULT_AGENT_SOCKET=/run/sub2api-payment-vault/public.sock",
		`UNIFIED_PAYMENT_WEBHOOK_PUBLIC_KEYS_JSON={"sub2.webhook.sandbox.v1":"` + webhookPublic + `"}`,
		"UNIFIED_PAYMENT_RETURN_URL=https://www.turtleligpt.com/payment/result",
	}, "\n") + "\n"
}

func clearMap(values map[string][]byte) {
	for key, value := range values {
		zero(value)
		delete(values, key)
	}
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func classification(output io.Writer, value string, code int) int {
	if output != nil {
		_, _ = fmt.Fprintln(output, value)
	}
	return code
}
