package shanghaidelivery

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/studiothumbnail"
	_ "golang.org/x/image/webp"
)

const (
	maxPendingBytes = 2 << 30
	maxSlots        = 4096
)

type Server struct {
	Root         string
	Controller   netip.Addr
	IngressURL   string
	COSHost      string
	ObjectPrefix string
	HTTP         *http.Client
	locks        [64]sync.Mutex
	capacity     sync.Mutex
	thumbnail    sync.Mutex
}

type ticket struct {
	Digest       [sha256.Size]byte `json:"digest"`
	Expires      time.Time         `json:"expires"`
	MiniJobID    string            `json:"mini_job_id,omitempty"`
	MiniFailed   bool              `json:"mini_failed,omitempty"`
	CloudClaimed bool              `json:"cloud_claimed,omitempty"`
	Prepare
}

func (s *Server) Control() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /v1/slots/{id}", s.status)
	mux.HandleFunc("POST /v1/slots/{id}/prepare", s.prepare)
	mux.HandleFunc("PUT /v1/slots/{id}/mini-job", s.recordMiniJob)
	mux.HandleFunc("PUT /v1/slots/{id}/mini-failed", s.recordMiniFailure)
	mux.HandleFunc("POST /v1/slots/{id}/cloud", s.claimCloud)
	mux.HandleFunc("PUT /v1/slots/{id}/data", s.stage)
	mux.HandleFunc("POST /v1/slots/{id}/publish", s.publish)
	mux.HandleFunc("GET /v1/slots/{id}/thumbnail", s.preview)
	mux.HandleFunc("DELETE /v1/slots/{id}", s.acknowledge)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		peer, err := netip.ParseAddr(host)
		if err != nil || peer.Unmap() != s.Controller {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) Ingress() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/incoming/{id}", s.receive)
	return mux
}

func (s *Server) withSlot(w http.ResponseWriter, r *http.Request, run func(string, string)) {
	id := r.PathValue("id")
	if !validID(id) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	lock := &s.locks[sha256.Sum256([]byte(id))[0]%byte(len(s.locks))]
	if !lock.TryLock() {
		w.WriteHeader(http.StatusConflict)
		return
	}
	defer lock.Unlock()
	if r.Context().Err() != nil {
		return
	}
	run(id, filepath.Join(s.Root, id))
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	s.withSlot(w, r, func(_ string, dir string) {
		slot, err := readSlot(dir)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, slot)
	})
}

func (s *Server) prepare(w http.ResponseWriter, r *http.Request) {
	var input Prepare
	if !readRequest(w, r, &input) || !ValidDimensions(input.Width, input.Height) || s.IngressURL == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.withSlot(w, r, func(id, dir string) {
		if slot, err := readSlot(dir); err != nil || slot.Image != nil || slot.CloudClaimed || slot.MiniExpires != nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		s.capacity.Lock()
		reserved := s.reserveLocked(dir, 0)
		s.capacity.Unlock()
		if !reserved {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		grant := base64.RawURLEncoding.EncodeToString(raw[:])
		record := ticket{Digest: sha256.Sum256([]byte(grant)), Expires: time.Now().Add(TicketTTL), Prepare: input}
		if err := writeState(dir, "ticket", record); err != nil || syncDir(s.Root) != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, Grant{URL: s.IngressURL + "/v1/incoming/" + id + "?ticket=" + grant})
	})
}

func (s *Server) recordMiniJob(w http.ResponseWriter, r *http.Request) {
	var job MiniJob
	if !readRequest(w, r, &job) || !job.Valid() {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.withSlot(w, r, func(_ string, dir string) {
		record, err := readTicket(dir)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if record.Expires.IsZero() || record.CloudClaimed || record.MiniFailed || (record.MiniJobID != "" && record.MiniJobID != job.ID) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if record.MiniJobID != job.ID {
			record.MiniJobID = job.ID
			if writeState(dir, "ticket", record) != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) recordMiniFailure(w http.ResponseWriter, r *http.Request) {
	var job MiniJob
	if !readRequest(w, r, &job) || (job.ID != "" && !job.Valid()) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.withSlot(w, r, func(_ string, dir string) {
		slot, err := readSlot(dir)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if slot.Image != nil || slot.MiniExpires == nil || slot.MiniJobID != job.ID || slot.CloudClaimed {
			w.WriteHeader(http.StatusConflict)
			return
		}
		record, err := readTicket(dir)
		record.MiniFailed = true
		if err != nil || writeState(dir, "ticket", record) != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) claimCloud(w http.ResponseWriter, r *http.Request) {
	s.withSlot(w, r, func(_ string, dir string) {
		slot, err := readSlot(dir)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if slot.Image != nil || slot.CloudClaimed || (slot.MiniExpires != nil && !slot.MiniFailed) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		record, err := readTicket(dir)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		s.capacity.Lock()
		reserved := s.reserveLocked(dir, 0)
		s.capacity.Unlock()
		record.CloudClaimed = true
		if !reserved || writeState(dir, "ticket", record) != nil || syncDir(s.Root) != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) stage(w http.ResponseWriter, r *http.Request) {
	s.withSlot(w, r, func(_ string, dir string) {
		record, err := readTicket(dir)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var expected Prepare
		miniJobID := r.URL.Query().Get("mini_job_id")
		if miniJobID != "" {
			if record.CloudClaimed || record.MiniFailed || record.MiniJobID != miniJobID {
				w.WriteHeader(http.StatusConflict)
				return
			}
			expected = record.Prepare
		} else if !record.Expires.IsZero() && !record.CloudClaimed {
			w.WriteHeader(http.StatusConflict)
			return
		}
		s.stageImage(w, r, dir, expected)
	})
}

func (s *Server) receive(w http.ResponseWriter, r *http.Request) {
	s.withSlot(w, r, func(_ string, dir string) {
		record, err := readTicket(dir)
		digest := sha256.Sum256([]byte(r.URL.Query().Get("ticket")))
		if err != nil || record.CloudClaimed || record.MiniFailed || !time.Now().Before(record.Expires) || subtle.ConstantTimeCompare(digest[:], record.Digest[:]) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		s.stageImage(w, r, dir, record.Prepare)
	})
}

func (s *Server) reserveLocked(dir string, size int64) bool {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return false
	}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) && len(entries) >= maxSlots {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validID(entry.Name()) {
			continue
		}
		// Reserve one maximum-sized input for another writer that is still streaming.
		if file, err := os.Stat(filepath.Join(s.Root, entry.Name(), "image.writing")); err == nil && file.Mode().IsRegular() {
			size += MaxImage
		}
		if file, err := os.Stat(filepath.Join(s.Root, entry.Name(), "image")); err == nil && file.Mode().IsRegular() {
			size += file.Size()
		}
		if size > maxPendingBytes {
			return false
		}
	}
	return os.Mkdir(dir, 0700) == nil || isDirectory(dir)
}

func isDirectory(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func (s *Server) stageImage(w http.ResponseWriter, r *http.Request, dir string, expected Prepare) {
	slot, err := readSlot(dir)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if slot.Image != nil {
		writeJSON(w, slot)
		return
	}
	if r.ContentLength <= 0 || r.ContentLength > MaxImage {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	name := filepath.Join(dir, "image.writing")
	f, err := s.openStagingFile(dir, name, r.ContentLength)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = f.Close(); _ = os.Remove(name) }()
	sha, md := sha256.New(), md5.New() // COS verifies single-part Content-MD5; SHA-256 identifies the staged bytes.
	n, err := io.Copy(io.MultiWriter(f, sha, md), http.MaxBytesReader(w, r.Body, MaxImage))
	if err != nil || n != r.ContentLength || r.Context().Err() != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	cfg, format, err := image.DecodeConfig(f)
	mime := "image/" + format
	if err != nil || Extension(mime) == "" || !ValidDimensions(cfg.Width, cfg.Height) ||
		(expected.Width != 0 && (expected.Width != cfg.Width || expected.Height != cfg.Height)) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	slot.Image = &Image{Bytes: n, Width: cfg.Width, Height: cfg.Height, MimeType: mime,
		SHA256: hex.EncodeToString(sha.Sum(nil)), MD5Base64: base64.StdEncoding.EncodeToString(md.Sum(nil))}
	if f.Sync() != nil || f.Close() != nil || writeState(dir, "slot", slot) != nil ||
		os.Rename(name, filepath.Join(dir, "image")) != nil || syncDir(dir) != nil || syncDir(s.Root) != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, slot)
}

func (s *Server) openStagingFile(dir, name string, size int64) (*os.File, error) {
	s.capacity.Lock()
	defer s.capacity.Unlock()
	if !s.reserveLocked(dir, size) {
		return nil, errors.New("delivery spool is at capacity")
	}
	return os.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
}

func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	var input Publish
	if !readRequest(w, r, &input) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.withSlot(w, r, func(id, dir string) {
		slot, err := readSlot(dir)
		if err != nil || slot.Image == nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		put, putOK := s.destination(input.PutURL, id, slot.Image.MimeType)
		head, headOK := s.destination(input.HeadURL, id, slot.Image.MimeType)
		if !putOK || !headOK || put.Path != head.Path {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), PutTimeout)
		defer cancel()
		equal, missing := s.matchObject(ctx, head, slot.Image)
		if !equal && missing {
			if !s.putObject(ctx, put, dir, slot.Image) {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			equal, _ = s.matchObject(ctx, head, slot.Image)
		}
		if !equal {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		slot.Published = true
		if writeState(dir, "slot", slot) != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, slot)
	})
}

func (s *Server) destination(raw, id, mime string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != s.COSHost || u.User != nil || u.Fragment != "" ||
		u.RawQuery == "" || u.RawPath != "" || u.Path != path.Clean(u.Path) {
		return nil, false
	}
	key := strings.TrimPrefix(u.Path, "/")
	expiration := u.Query().Get("X-Amz-Expires")
	if !strings.HasPrefix(key, s.ObjectPrefix+"/upscaled/") || SlotID(key) != id || !strings.HasSuffix(key, "/00."+Extension(mime)) ||
		u.Query().Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" || u.Query().Get("X-Amz-Signature") == "" || expiration == "" {
		return nil, false
	}
	seconds, err := strconv.Atoi(expiration)
	return u, err == nil && seconds > 0 && seconds <= 60
}

func (s *Server) matchObject(ctx context.Context, u *url.URL, expected *Image) (equal, missing bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	if err != nil {
		return false, false
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, true
	}
	digest := resp.Header.Get("X-Cos-Meta-Sub2-Sha256")
	if digest == "" {
		digest = resp.Header.Get("X-Amz-Meta-Sub2-Sha256")
	}
	return resp.StatusCode == http.StatusOK && resp.ContentLength == expected.Bytes &&
		resp.Header.Get("Content-Type") == expected.MimeType && digest == expected.SHA256, false
}

func (s *Server) putObject(ctx context.Context, u *url.URL, dir string, img *Image) bool {
	file, err := os.Open(filepath.Join(dir, "image"))
	if err != nil {
		return false
	}
	defer file.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), file)
	if err != nil {
		return false
	}
	req.ContentLength = img.Bytes
	req.Header.Set("Content-Type", img.MimeType)
	req.Header.Set("Content-MD5", img.MD5Base64)
	req.Header.Set("X-Cos-Meta-Sub2-Sha256", img.SHA256)
	req.Header.Set("X-Cos-Forbid-Overwrite", "true")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusConflict
}

func (s *Server) acknowledge(w http.ResponseWriter, r *http.Request) {
	s.withSlot(w, r, func(_ string, dir string) {
		slot, err := readSlot(dir)
		if err != nil || (!slot.Published && (slot.Image != nil || slot.MiniExpires != nil || slot.CloudClaimed)) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if os.RemoveAll(dir) != nil || syncDir(s.Root) != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	s.withSlot(w, r, func(_ string, dir string) {
		slot, err := readSlot(dir)
		if err != nil || !slot.Published {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if !s.thumbnail.TryLock() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		defer s.thumbnail.Unlock()
		f, err := os.Open(filepath.Join(dir, "image"))
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		defer f.Close()
		encoded, err := studiothumbnail.Encode(f)
		if err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
		_, _ = w.Write(encoded)
	})
}

func readSlot(dir string) (Slot, error) {
	var value Slot
	if _, err := os.Stat(filepath.Join(dir, "image")); err == nil {
		if err := readState(dir, "slot", &value); err != nil {
			return Slot{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Slot{}, err
	}
	record, err := readTicket(dir)
	if err != nil {
		return Slot{}, err
	}
	if !record.Expires.IsZero() {
		value.MiniExpires = &record.Expires
	}
	value.MiniJobID = record.MiniJobID
	value.MiniFailed = record.MiniFailed
	value.CloudClaimed = record.CloudClaimed
	return value, nil
}

func readTicket(dir string) (ticket, error) {
	var record ticket
	err := readState(dir, "ticket", &record)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return record, err
}

func readState(dir, name string, dst any) error {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func writeState(dir, name string, value any) error {
	f, err := os.CreateTemp(dir, ".state-")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if err = json.NewEncoder(f).Encode(value); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(dir, name))
	}
	if err == nil {
		err = syncDir(dir)
	}
	return err
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func readRequest(w http.ResponseWriter, r *http.Request, input any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	d.DisallowUnknownFields()
	return d.Decode(input) == nil && errors.Is(d.Decode(new(any)), io.EOF)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}
