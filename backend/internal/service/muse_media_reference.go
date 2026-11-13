package service

import (
	"bytes"
	"context"
	"errors"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"

	"golang.org/x/image/webp"
)

var museReferenceTypes = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif",
	".webp": "image/webp", ".heic": "image/heic", ".heif": "image/heif", ".avif": "image/avif",
	".mp4": "video/mp4", ".mov": "video/quicktime", ".webm": "video/webm", ".3gp": "video/3gpp",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".m4a": "audio/mp4", ".ogg": "audio/ogg",
	".flac": "audio/flac", ".aac": "audio/aac", ".opus": "audio/opus",
	".pdf": "application/pdf", ".doc": "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
}

var museReferenceAliases = map[string]string{
	"image/jpg": "image/jpeg", "image/pjpeg": "image/jpeg",
	"video/x-m4v": "video/mp4", "video/mov": "video/quicktime",
	"audio/mp3": "audio/mpeg", "audio/x-mp3": "audio/mpeg", "audio/mpeg3": "audio/mpeg",
	"audio/x-wav": "audio/wav", "audio/wave": "audio/wav", "audio/vnd.wave": "audio/wav",
	"audio/x-m4a": "audio/mp4", "audio/m4a": "audio/mp4", "audio/x-flac": "audio/flac",
	"audio/x-aac": "audio/aac", "audio/aacp": "audio/aac", "application/ogg": "audio/ogg",
}

func museReferenceMIME(data []byte, declared, name string) (string, error) {
	if len(data) == 0 {
		return "", errors.New("empty reference file")
	}
	mediaType, _, _ := mime.ParseMediaType(declared)
	if canonical := museReferenceAliases[mediaType]; canonical != "" {
		mediaType = canonical
	}
	detected := sniffMuseMedia(data)
	switch mediaType {
	case "", "application/octet-stream", "binary/octet-stream", "application/binary", "application/unknown":
		mediaType = detected
		if mediaType == "" {
			mediaType = museReferenceTypes[strings.ToLower(path.Ext(name))]
		}
	}
	allowed := false
	for _, supported := range museReferenceTypes {
		if supported == mediaType {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", errors.New("unsupported Muse reference format; use a supported image, video, audio, PDF or Word file")
	}
	// Never let a claimed audio or document type bypass the image audit.
	if strings.HasPrefix(detected, "image/") || strings.HasPrefix(mediaType, "image/") {
		if !validMuseImageType(data, mediaType, detected) {
			return "", errors.New("Muse reference does not match its declared image format")
		}
	}
	return mediaType, nil
}

func validMuseImageType(data []byte, mediaType, detected string) bool {
	if mediaType != detected &&
		!((mediaType == "image/heic" || mediaType == "image/heif" || mediaType == "image/avif") &&
			(detected == "image/heic" || detected == "image/heif" || detected == "image/avif")) {
		return false
	}
	switch mediaType {
	case "image/jpeg":
		_, err := jpeg.DecodeConfig(bytes.NewReader(data))
		return err == nil
	case "image/png":
		_, err := png.DecodeConfig(bytes.NewReader(data))
		return err == nil
	case "image/gif":
		_, err := gif.DecodeConfig(bytes.NewReader(data))
		return err == nil
	case "image/webp":
		_, err := webp.DecodeConfig(bytes.NewReader(data))
		return err == nil
	default:
		return strings.HasPrefix(mediaType, "image/")
	}
}

func sniffMuseMedia(data []byte) string {
	if len(data) >= 12 && string(data[4:8]) == "ftyp" {
		brand := string(data[8:12])
		switch brand {
		case "qt  ":
			return "video/quicktime"
		case "M4A ", "M4B ":
			return "audio/mp4"
		case "heic", "heix", "hevc", "hevx", "heim", "heis":
			return "image/heic"
		case "mif1", "msf1", "heif":
			return "image/heif"
		case "avif", "avis":
			return "image/avif"
		}
		if strings.HasPrefix(brand, "3g") {
			return "video/3gpp"
		}
		return "video/mp4"
	}
	switch {
	case bytes.HasPrefix(data, []byte("\x1a\x45\xdf\xa3")):
		return "video/webm"
	case bytes.HasPrefix(data, []byte("OggS")):
		if bytes.Contains(data[:min(len(data), 64)], []byte("OpusHead")) {
			return "audio/opus"
		}
		return "audio/ogg"
	case bytes.HasPrefix(data, []byte("fLaC")):
		return "audio/flac"
	case len(data) >= 2 && data[0] == 0xff && (data[1] == 0xf1 || data[1] == 0xf9):
		return "audio/aac"
	case len(data) >= 2 && data[0] == 0xff && (data[1] == 0xfb || data[1] == 0xf3 || data[1] == 0xf2):
		return "audio/mpeg"
	}
	detected := http.DetectContentType(data)
	switch detected {
	case "audio/wave":
		return "audio/wav"
	case "application/ogg":
		return "audio/ogg"
	}
	for _, supported := range museReferenceTypes {
		if supported == detected {
			return detected
		}
	}
	return ""
}

var museReferenceHTTP = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		DialContext:            dialMusePublicReference,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           16,
		IdleConnTimeout:        30 * time.Second,
		TLSHandshakeTimeout:    8 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 10 {
			return errors.New("too many Muse reference redirects")
		}
		req.Header.Del("Referer")
		return validateMuseReferenceURL(req.URL)
	},
}

func validateMuseReferenceURL(u *url.URL) error {
	if u == nil || u.User != nil || u.Opaque != "" || u.Hostname() == "" ||
		(u.Scheme != "http" && u.Scheme != "https") || strings.Contains(u.Hostname(), "%") {
		return errors.New("use a public HTTP(S) reference URL without embedded credentials")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if isBlockedHostname(host) || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return errors.New("Muse reference URL must resolve to a public address")
	}
	if address, err := netip.ParseAddr(host); err == nil && !isMusePublicAddress(address) {
		return errors.New("Muse reference URL must resolve to a public address")
	}
	return nil
}

var museNonPublicNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

var musePublicIPv6Range = netip.MustParsePrefix("2000::/3")
var museReferenceDialer = net.Dialer{Timeout: 8 * time.Second}

func isMusePublicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.Zone() != "" ||
		(address.Is6() && !musePublicIPv6Range.Contains(address)) {
		return false
	}
	for _, prefix := range museNonPublicNetworks {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func dialMusePublicReference(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("Muse reference host could not be resolved")
	}
	for _, ip := range addresses {
		if !isMusePublicAddress(ip) {
			return nil, errors.New("Muse reference URL must resolve to a public address")
		}
	}
	// Use the checked IP for the actual connection; net/http still verifies the
	// original TLS hostname. No environment proxy or second DNS lookup is used.
	for _, ip := range addresses {
		if conn, err := museReferenceDialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port)); err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("Muse reference host could not be reached")
}

func fetchMuseReference(ctx context.Context, rawURL string) ([]byte, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", "", errors.New("invalid Muse reference URL")
	}
	if err := validateMuseReferenceURL(req.URL); err != nil {
		return nil, "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := museReferenceHTTP.Do(req)
	if err != nil {
		return nil, "", "", errors.New("could not download the public Muse reference URL")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", "", errors.New("the public Muse reference URL did not return a file")
	}
	if resp.ContentLength > museMaxReferenceBytes {
		return nil, "", "", errMuseReferenceSize
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, museMaxReferenceBytes+1))
	if err != nil {
		return nil, "", "", errors.New("could not read the Muse reference file")
	}
	if len(data) > museMaxReferenceBytes {
		return nil, "", "", errMuseReferenceSize
	}
	return data, resp.Header.Get("Content-Type"), resp.Request.URL.Path, nil
}
