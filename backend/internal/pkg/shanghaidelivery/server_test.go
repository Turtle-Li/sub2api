package shanghaidelivery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

const testKey = "studio/prod/upscaled/job/cust/00.png"

func TestReconcileCommittedCOSObjectAfterConnectionLossAndRestart(t *testing.T) {
	var puts atomic.Int32
	var saved atomic.Bool
	var digest atomic.Value
	body := makePNG(t, 8, 16)
	cos := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+testKey {
			t.Error("COS path changed")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodPut:
			puts.Add(1)
			data, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(data, body) || r.Header.Get("X-Cos-Forbid-Overwrite") != "true" || r.Header.Get("Content-MD5") == "" {
				t.Error("COS payload or the integrity headers changed")
			}
			digest.Store(r.Header.Get("X-Cos-Meta-Sub2-Sha256"))
			saved.Store(true)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close() // COS committed, but the PUT response never arrived.
			}
		case http.MethodHead:
			if !saved.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("X-Cos-Meta-Sub2-Sha256", digest.Load().(string))
		}
	}))
	defer cos.Close()
	root := t.TempDir()
	newSpool := func() *Server {
		return &Server{Root: root, Controller: netip.MustParseAddr("100.64.0.2"), COSHost: cos.Listener.Addr().String(), ObjectPrefix: "studio/prod", HTTP: cos.Client()}
	}
	ctl := newSpool().Control()
	p := "/v1/slots/" + SlotID(testKey)
	call(t, ctl, http.MethodPut, p+"/data", body, http.StatusOK)
	url := cos.URL + "/" + testKey + "?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Expires=60&X-Amz-Signature=fake"
	input := jsonBytes(t, Publish{PutURL: url, HeadURL: url})
	call(t, ctl, http.MethodPost, p+"/publish", input, http.StatusBadGateway)
	ctl = newSpool().Control()
	resp := call(t, ctl, http.MethodPost, p+"/publish", input, http.StatusOK)
	var recovered Slot
	if err := json.Unmarshal(resp, &recovered); err != nil || !recovered.Published || recovered.Image == nil || recovered.Image.SHA256 != sha(body) {
		t.Fatalf("invalid recovered slot: %+v, %v", recovered, err)
	}
	if puts.Load() != 1 {
		t.Fatalf("second request repeated the same COS PUT: got %d puts", puts.Load())
	}
	preview := call(t, ctl, http.MethodGet, p+"/thumbnail", nil, http.StatusOK)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(preview))
	if err != nil || format != "jpeg" || cfg.Width != 8 || cfg.Height != 16 {
		t.Fatalf("thumbnail did not retain the expected aspect: %+v %s %v", cfg, format, err)
	}
	call(t, ctl, http.MethodDelete, p, nil, http.StatusNoContent)
	resp = call(t, ctl, http.MethodGet, p, nil, http.StatusOK)
	if err := json.Unmarshal(resp, &recovered); err != nil || bytes.Contains(resp, []byte("image")) {
		t.Fatal("acknowledgement left staged image on disk")
	}
}

func TestExpiredMiniTicketDoesNotClearTheAttemptFence(t *testing.T) {
	s := &Server{Root: t.TempDir(), Controller: netip.MustParseAddr("100.64.0.2"), IngressURL: "https://private.ts.net:18488"}
	ctl := s.Control()
	id := SlotID(testKey)
	p := "/v1/slots/" + id
	call(t, ctl, http.MethodPost, p+"/prepare", jsonBytes(t, Prepare{Width: 8, Height: 16}), http.StatusOK)
	dir := filepath.Join(s.Root, id)
	prepared, err := readTicket(dir)
	if err != nil {
		t.Fatal(err)
	}
	prepared.Expires = time.Now().Add(-time.Minute)
	if err := writeState(dir, "ticket", prepared); err != nil {
		t.Fatal(err)
	}
	call(t, ctl, http.MethodPost, p+"/cloud", nil, http.StatusConflict)
	call(t, ctl, http.MethodPost, p+"/prepare", jsonBytes(t, Prepare{Width: 8, Height: 16}), http.StatusConflict)
	job := MiniJob{ID: "aaee866b-1475-4980-aafb-5fc6351d3e18"}
	call(t, ctl, http.MethodPut, p+"/mini-job", jsonBytes(t, job), http.StatusNoContent)
	call(t, ctl, http.MethodPut, p+"/data?mini_job_id=other", makePNG(t, 8, 16), http.StatusConflict)
	call(t, ctl, http.MethodPut, p+"/data?mini_job_id="+job.ID, makePNG(t, 16, 8), http.StatusUnprocessableEntity)
	call(t, ctl, http.MethodPut, p+"/data?mini_job_id="+job.ID, makePNG(t, 8, 16), http.StatusOK)
	call(t, ctl, http.MethodPut, p+"/mini-failed", jsonBytes(t, job), http.StatusConflict)
	call(t, ctl, http.MethodPost, p+"/cloud", nil, http.StatusConflict)
}

func TestScopedMiniGrantValidatesDimensionsAndDoesNotReplaceStagedResult(t *testing.T) {
	s := &Server{Root: t.TempDir(), Controller: netip.MustParseAddr("100.64.0.2"), IngressURL: "https://private.ts.net:18488"}
	ctl := s.Control()
	id := SlotID(testKey)
	p := "/v1/slots/" + id
	prep := jsonBytes(t, Prepare{Width: 8, Height: 16})
	var grant Grant
	if err := json.Unmarshal(call(t, ctl, http.MethodPost, p+"/prepare", prep, http.StatusOK), &grant); err != nil {
		t.Fatal(err)
	}
	call(t, ctl, http.MethodPost, p+"/prepare", prep, http.StatusConflict)
	call(t, s.Ingress(), http.MethodPut, s.IngressURL+"/v1/incoming/"+id+"?ticket=bad", makePNG(t, 8, 16), http.StatusForbidden)
	call(t, s.Ingress(), http.MethodPut, grant.URL, makePNG(t, 16, 8), http.StatusUnprocessableEntity)
	body := makePNG(t, 8, 16)
	call(t, s.Ingress(), http.MethodPut, grant.URL, body, http.StatusOK)
	call(t, s.Ingress(), http.MethodPut, grant.URL, makePNG(t, 16, 8), http.StatusOK)
	call(t, ctl, http.MethodDelete, p, nil, http.StatusConflict)
	resp := call(t, ctl, http.MethodGet, p, nil, http.StatusOK)
	var got Slot
	if err := json.Unmarshal(resp, &got); err != nil || got.Image == nil || got.Image.SHA256 != sha(body) {
		t.Fatal("valid staged bytes were not recoverable through an authorized receipt")
	}
}

func TestRejectedControlPeerAndForeignCOSDestination(t *testing.T) {
	s := &Server{Root: t.TempDir(), Controller: netip.MustParseAddr("100.64.0.2"), COSHost: "cos.example.com", ObjectPrefix: "studio/prod"}
	ctl := s.Control()
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.RemoteAddr = "100.64.0.3:5000"
	r.Header.Set("X-Forwarded-For", "100.64.0.2")
	w := httptest.NewRecorder()
	ctl.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("another Tailnet peer could use the control plane")
	}
	p := "/v1/slots/" + SlotID(testKey)
	call(t, ctl, http.MethodPut, p+"/data", makePNG(t, 8, 16), http.StatusOK)
	bad := "https://foreign.example.com/" + testKey + "?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Expires=60&X-Amz-Signature=fake"
	call(t, ctl, http.MethodPost, p+"/publish", jsonBytes(t, Publish{PutURL: bad, HeadURL: bad}), http.StatusBadRequest)
}

func call(t *testing.T, handler http.Handler, verb, path string, payload []byte, status int) []byte {
	t.Helper()
	r := httptest.NewRequest(verb, path, bytes.NewReader(payload))
	r.RemoteAddr = "100.64.0.2:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: got status %d, want %d", verb, r.URL.Path, w.Code, status)
	}
	return w.Body.Bytes()
}

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jsonBytes(t *testing.T, input any) []byte {
	t.Helper()
	b, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sha(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
