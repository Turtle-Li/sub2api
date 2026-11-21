package shanghaidelivery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	ControlPort = "18486"
	IngressPort = "18488"
	MaxImage    = 48 << 20
	PutTimeout  = 30 * time.Second
	TicketTTL   = 20 * time.Minute
)

var tailnetIPv4 = netip.MustParsePrefix("100.64.0.0/10")

func IsTailnetIP(ip netip.Addr) bool { return tailnetIPv4.Contains(ip) }

func ValidateControlURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.Port() != ControlPort || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("Shanghai delivery control must be a Tailnet IPv4 origin on port " + ControlPort)
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !IsTailnetIP(ip) {
		return errors.New("Shanghai delivery control address is not a Tailnet IPv4 address")
	}
	return nil
}

func SlotID(key string) string {
	stem := key
	if index := strings.LastIndexByte(key, '.'); index > strings.LastIndexByte(key, '/') {
		stem = key[:index]
	}
	sum := sha256.Sum256([]byte(stem))
	return hex.EncodeToString(sum[:])
}

func validID(id string) bool {
	if len(id) != sha256.Size*2 || id != strings.ToLower(id) {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

type Image struct {
	Bytes     int64  `json:"bytes"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	MimeType  string `json:"mime_type"`
	SHA256    string `json:"sha256"`
	MD5Base64 string `json:"md5_base64"`
}

type Slot struct {
	Image        *Image     `json:"image,omitempty"`
	Published    bool       `json:"published"`
	MiniExpires  *time.Time `json:"mini_expires,omitempty"`
	MiniJobID    string     `json:"mini_job_id,omitempty"`
	MiniFailed   bool       `json:"mini_failed,omitempty"`
	CloudClaimed bool       `json:"cloud_claimed,omitempty"`
}

type Prepare struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type Grant struct {
	URL string `json:"url"`
}

type MiniJob struct {
	ID string `json:"id"`
}

func (j MiniJob) Valid() bool {
	if len(j.ID) != 36 || j.ID[8] != '-' || j.ID[13] != '-' || j.ID[18] != '-' || j.ID[23] != '-' {
		return false
	}
	hexID := strings.ReplaceAll(j.ID, "-", "")
	if len(hexID) != 32 || hexID != strings.ToLower(hexID) {
		return false
	}
	_, err := hex.DecodeString(hexID)
	return err == nil
}

type Publish struct {
	PutURL  string `json:"put_url"`
	HeadURL string `json:"head_url"`
}

func Extension(mime string) string {
	switch mime {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	default:
		return ""
	}
}

func ValidDimensions(w, h int) bool {
	return w > 0 && h > 0 && w <= 8192 && h <= 8192 && int64(w)*int64(h) <= 33554432
}
