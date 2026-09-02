// Command sub2api-vault-agent is a small memory-only bridge between one
// owner-approved Vault injection and Sub2's product request signer. It never
// opens Vaultwarden, accepts a token, or persists a secret. A restart returns
// it to a locked state.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultPublicSocket     = "/run/sub2api-payment-vault/public.sock"
	defaultAdminSocket      = "/run/sub2api-payment-vault-admin/admin.sock"
	maxInjectedSecretBytes  = 512
	maxAgentResponseBytes   = 4 * 1024
	maxAllowedReferences    = 4
	agentShutdownTimeout    = 5 * time.Second
	agentHTTPClientTimeout  = 5 * time.Second
	agentMaxHeaderBytes     = 8 * 1024
	agentReadHeaderTimeout  = 2 * time.Second
	agentReadRequestTimeout = 5 * time.Second
)

var (
	errAgentConfiguration = errors.New("sub2api vault agent configuration invalid")
	errAgentReference     = errors.New("sub2api vault reference invalid")
	errAgentSecret        = errors.New("sub2api vault secret invalid")
	errAgentUnavailable   = errors.New("sub2api vault agent unavailable")
)

type reference struct {
	path  string
	field string
}

type referenceList []string

func (values *referenceList) String() string { return strings.Join(*values, ",") }

func (values *referenceList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout))
}

func run(args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		return 2
	}
	switch args[0] {
	case "serve":
		flags := flag.NewFlagSet("sub2api-vault-agent serve", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		publicSocket := flags.String("public-socket", defaultPublicSocket, "read-only Vault KV socket")
		adminSocket := flags.String("admin-socket", defaultAdminSocket, "private injection socket")
		var allowed referenceList
		flags.Var(&allowed, "allowed-ref", "exact vault://path#field allowed for this agent")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
			return 2
		}
		store, err := newMemoryVault(allowed)
		if err != nil || validateSocketPath(*publicSocket) != nil || validateSocketPath(*adminSocket) != nil || *publicSocket == *adminSocket {
			return 2
		}
		defer store.clear()
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := serve(ctx, *publicSocket, *adminSocket, store); err != nil {
			_, _ = fmt.Fprintln(stdout, "SUB2API_VAULT_AGENT_STOPPED")
			return 1
		}
		return 0
	case "load":
		flags := flag.NewFlagSet("sub2api-vault-agent load", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		adminSocket := flags.String("admin-socket", defaultAdminSocket, "private injection socket")
		rawReference := flags.String("ref", "", "exact vault://path#field")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || validateSocketPath(*adminSocket) != nil {
			return 2
		}
		ref, err := parseReference(*rawReference)
		if err != nil {
			return 2
		}
		secret, err := readBoundedSecret(stdin)
		if err != nil {
			return 2
		}
		defer zero(secret)
		if err := loadOverUnix(context.Background(), *adminSocket, ref, secret); err != nil {
			return 1
		}
		_, _ = fmt.Fprintln(stdout, "SUB2API_VAULT_AGENT_LOADED")
		return 0
	case "check":
		flags := flag.NewFlagSet("sub2api-vault-agent check", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		publicSocket := flags.String("public-socket", defaultPublicSocket, "read-only Vault KV socket")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || validateSocketPath(*publicSocket) != nil {
			return 2
		}
		if err := checkReady(context.Background(), *publicSocket); err != nil {
			return 1
		}
		return 0
	default:
		return 2
	}
}

type memoryVault struct {
	mu      sync.RWMutex
	allowed map[string]map[string]struct{}
	values  map[string]map[string][]byte
}

func newMemoryVault(rawReferences []string) (*memoryVault, error) {
	if len(rawReferences) == 0 || len(rawReferences) > maxAllowedReferences {
		return nil, errAgentConfiguration
	}
	allowed := make(map[string]map[string]struct{}, len(rawReferences))
	for _, raw := range rawReferences {
		ref, err := parseReference(raw)
		if err != nil {
			return nil, errAgentConfiguration
		}
		fields := allowed[ref.path]
		if fields == nil {
			fields = make(map[string]struct{})
			allowed[ref.path] = fields
		}
		if _, duplicate := fields[ref.field]; duplicate {
			return nil, errAgentConfiguration
		}
		fields[ref.field] = struct{}{}
	}
	return &memoryVault{allowed: allowed, values: make(map[string]map[string][]byte)}, nil
}

func (vault *memoryVault) load(ref reference, secret []byte) error {
	if vault == nil || !validSecret(secret) {
		return errAgentSecret
	}
	vault.mu.Lock()
	defer vault.mu.Unlock()
	fields, ok := vault.allowed[ref.path]
	if !ok {
		return errAgentReference
	}
	if _, ok := fields[ref.field]; !ok {
		return errAgentReference
	}
	values := vault.values[ref.path]
	if values == nil {
		values = make(map[string][]byte)
		vault.values[ref.path] = values
	}
	if previous := values[ref.field]; previous != nil {
		zero(previous)
	}
	values[ref.field] = bytes.Clone(secret)
	return nil
}

func (vault *memoryVault) payload(path string) ([]byte, bool, bool) {
	if vault == nil {
		return nil, false, false
	}
	vault.mu.RLock()
	defer vault.mu.RUnlock()
	allowedFields, allowed := vault.allowed[path]
	if !allowed {
		return nil, false, false
	}
	loaded := vault.values[path]
	if len(loaded) != len(allowedFields) {
		return nil, true, false
	}
	fields := make(map[string]string, len(allowedFields))
	for field := range allowedFields {
		value := loaded[field]
		if !validSecret(value) {
			return nil, true, false
		}
		fields[field] = string(value)
	}
	payload, err := json.Marshal(struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}{Data: struct {
		Data map[string]string `json:"data"`
	}{Data: fields}})
	if err != nil || len(payload) > maxAgentResponseBytes {
		zero(payload)
		return nil, true, false
	}
	return payload, true, true
}

func (vault *memoryVault) ready() bool {
	if vault == nil {
		return false
	}
	vault.mu.RLock()
	defer vault.mu.RUnlock()
	for path, fields := range vault.allowed {
		loaded := vault.values[path]
		if len(loaded) != len(fields) {
			return false
		}
		for field := range fields {
			if !validSecret(loaded[field]) {
				return false
			}
		}
	}
	return len(vault.allowed) > 0
}

func (vault *memoryVault) clear() {
	if vault == nil {
		return
	}
	vault.mu.Lock()
	defer vault.mu.Unlock()
	for _, fields := range vault.values {
		for _, value := range fields {
			zero(value)
		}
	}
	vault.values = make(map[string]map[string][]byte)
}

func serve(ctx context.Context, publicSocket, adminSocket string, vault *memoryVault) error {
	if ctx == nil || vault == nil || validateSocketPath(publicSocket) != nil || validateSocketPath(adminSocket) != nil || publicSocket == adminSocket {
		return errAgentConfiguration
	}
	publicListener, err := listenPrivateUnix(publicSocket)
	if err != nil {
		return errAgentUnavailable
	}
	defer func() {
		_ = publicListener.Close()
		removeOwnedSocket(publicSocket)
	}()
	adminListener, err := listenPrivateUnix(adminSocket)
	if err != nil {
		return errAgentUnavailable
	}
	defer func() {
		_ = adminListener.Close()
		removeOwnedSocket(adminSocket)
	}()

	publicServer := hardenedServer(vault.readHandler())
	adminServer := hardenedServer(vault.adminHandler())
	errorsChannel := make(chan error, 2)
	go func() { errorsChannel <- publicServer.Serve(publicListener) }()
	go func() { errorsChannel <- adminServer.Serve(adminListener) }()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errorsChannel:
		if errors.Is(runErr, http.ErrServerClosed) {
			runErr = nil
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), agentShutdownTimeout)
	defer cancel()
	return errors.Join(runErr, publicServer.Shutdown(shutdownContext), adminServer.Shutdown(shutdownContext))
}

func hardenedServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler: handler, ReadHeaderTimeout: agentReadHeaderTimeout, ReadTimeout: agentReadRequestTimeout,
		WriteTimeout: agentReadRequestTimeout, IdleTimeout: 15 * time.Second, MaxHeaderBytes: agentMaxHeaderBytes,
	}
}

func (vault *memoryVault) readHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		secureAgentHeaders(writer)
		if request.Method != http.MethodGet || request.Host != "vault" || request.URL.RawQuery != "" || request.URL.Fragment != "" ||
			request.Header.Get("Authorization") != "" || request.Header.Get("X-Vault-Token") != "" || request.Header.Get("Cookie") != "" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		switch request.URL.Path {
		case "/health/live":
			writer.WriteHeader(http.StatusNoContent)
			return
		case "/health/ready":
			if vault.ready() {
				writer.WriteHeader(http.StatusNoContent)
				return
			}
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if !strings.HasPrefix(request.URL.Path, "/v1/") {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		path := strings.TrimPrefix(request.URL.Path, "/v1/")
		if !validVaultPath(path) {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		payload, allowed, ready := vault.payload(path)
		defer zero(payload)
		switch {
		case !allowed:
			writer.WriteHeader(http.StatusNotFound)
		case !ready:
			writer.WriteHeader(http.StatusServiceUnavailable)
		default:
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write(payload)
		}
	})
}

func (vault *memoryVault) adminHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		secureAgentHeaders(writer)
		const prefix = "/v1/load/"
		if request.Method != http.MethodPut || request.Host != "unix" || !strings.HasPrefix(request.URL.Path, prefix) ||
			request.Header.Get("Content-Type") != "application/octet-stream" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		path := strings.TrimPrefix(request.URL.Path, prefix)
		values, err := url.ParseQuery(request.URL.RawQuery)
		if err != nil || len(values) != 1 || len(values["field"]) != 1 {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		ref := reference{path: path, field: values.Get("field")}
		if !validVaultPath(ref.path) || !validVaultField(ref.field) {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		secret, err := readBoundedSecret(request.Body)
		if err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		defer zero(secret)
		if err := vault.load(ref, secret); err != nil {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
}

func secureAgentHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
}

func parseReference(raw string) (reference, error) {
	const prefix = "vault://"
	if raw == "" || strings.TrimSpace(raw) != raw || !strings.HasPrefix(raw, prefix) || strings.Count(raw, "#") != 1 {
		return reference{}, errAgentReference
	}
	parts := strings.SplitN(strings.TrimPrefix(raw, prefix), "#", 2)
	if !validVaultPath(parts[0]) || !validVaultField(parts[1]) {
		return reference{}, errAgentReference
	}
	return reference{path: parts[0], field: parts[1]}, nil
}

func validVaultPath(path string) bool {
	if path == "" || len(path) > 512 || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." || len(segment) > 128 {
			return false
		}
		for _, character := range segment {
			if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
				character >= '0' && character <= '9' || strings.ContainsRune("-._~", character)) {
				return false
			}
		}
	}
	return true
}

func validVaultField(field string) bool {
	if field == "" || len(field) > 128 {
		return false
	}
	for index, character := range field {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_' ||
			index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func validSecret(secret []byte) bool {
	return len(secret) > 0 && len(secret) <= maxInjectedSecretBytes && !bytes.ContainsRune(secret, '\x00')
}

func readBoundedSecret(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, errAgentSecret
	}
	content, err := io.ReadAll(io.LimitReader(reader, maxInjectedSecretBytes+1))
	if err != nil || !validSecret(content) {
		zero(content)
		return nil, errAgentSecret
	}
	return content, nil
}

func validateSocketPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Ext(path) != ".sock" || strings.ContainsRune(path, '\x00') {
		return errAgentConfiguration
	}
	return nil
}

func listenPrivateUnix(path string) (*net.UnixListener, error) {
	if validateSocketPath(path) != nil {
		return nil, errAgentConfiguration
	}
	directory := filepath.Dir(path)
	detail, err := os.Lstat(directory)
	if err != nil || !detail.IsDir() || detail.Mode()&os.ModeSymlink != 0 || detail.Mode().Perm()&0o077 != 0 {
		return nil, errAgentConfiguration
	}
	if owner, ok := fileOwnerUID(detail); !ok || owner != os.Getuid() {
		return nil, errAgentConfiguration
	}
	if existing, err := os.Lstat(path); err == nil {
		owner, ok := fileOwnerUID(existing)
		if existing.Mode()&os.ModeSocket == 0 || existing.Mode()&os.ModeSymlink != 0 || !ok || owner != os.Getuid() {
			return nil, errAgentConfiguration
		}
		if err := os.Remove(path); err != nil {
			return nil, errAgentUnavailable
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errAgentUnavailable
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, errAgentUnavailable
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		removeOwnedSocket(path)
		return nil, errAgentUnavailable
	}
	return listener, nil
}

func removeOwnedSocket(path string) {
	detail, err := os.Lstat(path)
	if err != nil || detail.Mode()&os.ModeSocket == 0 || detail.Mode()&os.ModeSymlink != 0 {
		return
	}
	owner, ok := fileOwnerUID(detail)
	if ok && owner == os.Getuid() {
		_ = os.Remove(path)
	}
}

func fileOwnerUID(detail os.FileInfo) (int, bool) {
	owner, ok := detail.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(owner.Uid), true
}

func unixHTTPClient(socket string) *http.Client {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(dialContext context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(dialContext, "unix", socket)
		},
		DisableCompression: true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   agentHTTPClientTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func loadOverUnix(ctx context.Context, socket string, ref reference, secret []byte) error {
	if ctx == nil || validateSocketPath(socket) != nil || !validVaultPath(ref.path) || !validVaultField(ref.field) || !validSecret(secret) {
		return errAgentConfiguration
	}
	client := unixHTTPClient(socket)
	requestURL := "http://unix/v1/load/" + ref.path + "?field=" + url.QueryEscape(ref.field)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, requestURL, bytes.NewReader(secret))
	if err != nil {
		return errAgentUnavailable
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := client.Do(request)
	if err != nil || response == nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return errAgentUnavailable
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errAgentUnavailable
	}
	return nil
}

func checkReady(ctx context.Context, publicSocket string) error {
	if ctx == nil || validateSocketPath(publicSocket) != nil {
		return errAgentConfiguration
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://vault/health/ready", nil)
	if err != nil {
		return errAgentUnavailable
	}
	response, err := unixHTTPClient(publicSocket).Do(request)
	if err != nil || response == nil {
		return errAgentUnavailable
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errAgentUnavailable
	}
	return nil
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
