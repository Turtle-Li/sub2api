package shopaction

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func ValidateIntegration(i Integration) error {
	if !identifier.MatchString(i.ID) || i.Version <= 0 {
		return errors.New("invalid integration identity")
	}
	u, err := url.Parse(i.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid integration endpoint")
	}
	switch i.Adapter {
	case HTTP:
		if !identifier.MatchString(i.CredentialAlias) {
			return errors.New("invalid credential alias")
		}
		if _, err = urlvalidator.ValidateHTTPURL(i.Endpoint, false, urlvalidator.ValidationOptions{AllowedHosts: []string{u.Hostname()}, RequireAllowlist: true}); err != nil {
			return errors.New("invalid public endpoint")
		}
		if i.ReadyEndpoint != "" {
			r, e := url.Parse(i.ReadyEndpoint)
			if e != nil || r.Scheme != "https" || r.Host != u.Host || r.User != nil || r.RawQuery != "" || r.Fragment != "" {
				return errors.New("invalid readiness endpoint")
			}
		}
	case Native:
		// This existing private adapter is distinct from arbitrary HTTP hooks.
		ip, e := netip.ParseAddr(u.Hostname())
		if e != nil || !ip.Is4() || ip.As4()[0] != 100 || ip.As4()[1] < 64 || ip.As4()[1] > 127 || u.Scheme != "http" || u.Port() == "" || (u.Path != "" && u.Path != "/") {
			return errors.New("native endpoint must be an approved Tailnet listener")
		}
	default:
		return errors.New("unsupported adapter")
	}
	return nil
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is4() {
		b := ip.As4()
		return b[0] != 0 && b[0] < 224 && ip.String() != "168.63.129.16" && !(b[0] == 100 && b[1] >= 64 && b[1] <= 127) && !(b[0] == 198 && (b[1] == 18 || b[1] == 19))
	}
	b := ip.As16()
	return b[0]&0xe0 == 0x20 && !(b[0] == 0x20 && b[1] == 0x02) && !(b[0] == 0x20 && b[1] == 0x01 && b[2] == 0 && b[3] == 0)
}

type publicDialer struct {
	lookup func(context.Context, string, string) ([]netip.Addr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
}

func (d publicDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid destination")
	}
	ips, err := d.lookup(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("destination resolution failed")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, errors.New("destination blocked")
		}
	}
	for _, ip := range ips {
		conn, e := d.dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
		err = e
	}
	return nil, err
}

func webhookHTTPClient() *http.Client {
	d := publicDialer{lookup: net.DefaultResolver.LookupNetIP, dial: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{Proxy: nil, DisableCompression: true, MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1, ResponseHeaderTimeout: 10 * time.Second, DialContext: d.DialContext}}
}
