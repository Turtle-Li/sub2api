package shopaction

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicDialPinsValidatedAddressesAndRejectsMixedDNS(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.100.100.200", "168.63.129.16", "::1", "fd00:ec2::254", "64:ff9b::a00:1", "::ffff:127.0.0.1"} {
		t.Run(address, func(t *testing.T) {
			called := false
			d := publicDialer{lookup: func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr(address)}, nil
			}, dial: func(context.Context, string, string) (net.Conn, error) { called = true; return nil, nil }}
			_, err := d.DialContext(context.Background(), "tcp", "hooks.example.com:443")
			require.Error(t, err)
			require.False(t, called)
		})
	}
	d := publicDialer{lookup: func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}, dial: func(_ context.Context, _ string, destination string) (net.Conn, error) {
		require.Equal(t, "1.1.1.1:443", destination)
		a, b := net.Pipe()
		b.Close()
		return a, nil
	}}
	conn, err := d.DialContext(context.Background(), "tcp", "hooks.example.com:443")
	require.NoError(t, err)
	conn.Close()
}

func TestIntegrationDestinationAndCredentialBoundaries(t *testing.T) {
	base := Integration{ID: "service", Version: 1, Adapter: HTTP, Endpoint: "https://hooks.example.com/deliver", CredentialAlias: "service-signing"}
	require.NoError(t, ValidateIntegration(base))
	for _, endpoint := range []string{"http://hooks.example.com", "https://user:secret@hooks.example.com", "https://127.0.0.1/hook", "https://localhost/hook", "https://hooks.example.com/hook?token=secret", "https://hooks.example.com/hook#fragment"} {
		i := base
		i.Endpoint = endpoint
		require.Error(t, ValidateIntegration(i), endpoint)
	}
	i := base
	i.ReadyEndpoint = "https://other.example.com/ready"
	require.Error(t, ValidateIntegration(i))
	i = base
	i.CredentialAlias = "../payment"
	require.Error(t, ValidateIntegration(i))
}
