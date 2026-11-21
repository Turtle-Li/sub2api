package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/shanghaidelivery"
)

func main() {
	listen, err := netip.ParseAddr(os.Getenv("IMAGE_DELIVERY_LOCAL_TAILNET_IP"))
	if err != nil || !shanghaidelivery.IsTailnetIP(listen) {
		log.Fatal("invalid local Tailnet address")
	}
	controller, err := netip.ParseAddr(os.Getenv("IMAGE_DELIVERY_AWS_TAILNET_IP"))
	if err != nil || !shanghaidelivery.IsTailnetIP(controller) || controller == listen {
		log.Fatal("invalid AWS Tailnet address")
	}
	ingress := os.Getenv("IMAGE_DELIVERY_INGRESS_URL")
	if ingress != "" {
		u, parseErr := url.Parse(ingress)
		if parseErr != nil || u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".ts.net") || u.Port() != shanghaidelivery.IngressPort ||
			u.Path != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			log.Fatal("invalid private Mini upload origin")
		}
	}
	host := os.Getenv("IMAGE_DELIVERY_COS_HOST")
	prefix := os.Getenv("IMAGE_DELIVERY_COS_PREFIX")
	if !strings.HasSuffix(host, ".cos.ap-shanghai.myqcloud.com") || strings.ContainsAny(host, "/:@?#%") ||
		prefix == "" || strings.HasPrefix(prefix, "/") || prefix == "." || strings.Contains(prefix, "..") || prefix != path.Clean(prefix) {
		log.Fatal("invalid Shanghai COS host or prefix")
	}
	root := os.Getenv("STATE_DIRECTORY")
	if root == "" || !path.IsAbs(root) || strings.Contains(root, ":") {
		log.Fatal("missing private systemd state directory")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	transport.DialContext = (&net.Dialer{Timeout: 4 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 4 * time.Second
	transport.ResponseHeaderTimeout = 12 * time.Second
	transfer := &shanghaidelivery.Server{Root: root, Controller: controller, IngressURL: ingress, COSHost: host, ObjectPrefix: prefix,
		HTTP: &http.Client{Transport: transport, Timeout: shanghaidelivery.PutTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	control := server(net.JoinHostPort(listen.String(), shanghaidelivery.ControlPort), transfer.Control())
	servers := []*http.Server{control}
	if ingress != "" {
		servers = append(servers, server("127.0.0.1:18487", transfer.Ingress()))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fail := make(chan error, len(servers))
	for _, srv := range servers {
		ln, err := net.Listen("tcp4", srv.Addr)
		if err != nil {
			log.Fatal("delivery listener could not be opened")
		}
		go func() { fail <- srv.Serve(ln) }()
	}
	fmt.Println("Shanghai image delivery ready")
	select {
	case <-ctx.Done():
	case <-fail:
		log.Fatal("delivery listener stopped unexpectedly")
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	for _, srv := range servers {
		_ = srv.Shutdown(shutdown)
	}
}

func server(addr string, handler http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, MaxHeaderBytes: 16 << 10,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 50 * time.Second, WriteTimeout: 55 * time.Second,
		IdleTimeout: 15 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
}
