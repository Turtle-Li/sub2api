package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	api "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	imageenhan "github.com/alibabacloud-go/imageenhan-20190930/v3/client"
	"github.com/alibabacloud-go/tea/dara"
)

const aliyunUpscaleVaultRef = "vault://secret/data/sub2api/aliyun-image-upscale#credentials"
const aliyunOSSRegion = "oss-cn-shanghai.aliyuncs.com"

var aliyunBucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

type aliyunUpscaler struct {
	http     *http.Client
	load     imageUpscaleAPIKeyLoader
	maxBytes int64
}

func newAliyunImageUpscaler(cfg config.ImageUpscaleConfig) func(context.Context, []byte, int, func() error) (*ImageUpscaleResult, error) {
	limit := cfg.MaxResultBytes
	if limit <= 0 {
		limit = 128 << 20
	}
	socket := cfg.AliyunAgentSocket
	if socket == "" {
		socket = cfg.VaultAgentSocket
	}
	return (&aliyunUpscaler{http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, load: newImageUpscaleVaultLoader(socket, aliyunUpscaleVaultRef), maxBytes: limit}).upscaleWithPaidGate
}

// The generated Advance method builds a second upload client without the caller's
// context. Use its AuthorizeFileUpload/OSS contract explicitly, then the signed
// generated WithContext method. No retries or redirects on the paid request.
func (a *aliyunUpscaler) upscale(ctx context.Context, source []byte, scale int) (*ImageUpscaleResult, error) {
	return a.upscaleWithPaidGate(ctx, source, scale, nil)
}

func (a *aliyunUpscaler) upscaleWithPaidGate(ctx context.Context, source []byte, scale int, beforePaid func() error) (*ImageUpscaleResult, error) {
	if ctx.Err() != nil {
		return nil, imageUpscaleError("JOB_TIMEOUT", 0, false, ctx.Err())
	}
	in, err := prepareAliyunInput(source)
	if err != nil {
		return nil, err
	}
	cfg, err := a.sdkConfig(ctx)
	if err != nil {
		return nil, err
	}
	runtime := &dara.RuntimeOptions{Autoretry: dara.Bool(false), MaxAttempts: dara.Int(1)}
	sourceURL, err := a.stage(ctx, cfg, runtime, in.data)
	if err != nil {
		return nil, err
	}
	cfg.Endpoint = dara.String("imageenhan.cn-shanghai.aliyuncs.com")
	client, err := imageenhan.NewClient(cfg)
	if err != nil {
		return nil, aliFailure("CLIENT")
	}
	if beforePaid != nil {
		if err := beforePaid(); err != nil {
			return nil, err
		}
	}
	resp, err := client.MakeSuperResolutionImageWithContext(ctx, &imageenhan.MakeSuperResolutionImageRequest{Url: &sourceURL, UpscaleFactor: dara.Int64(int64(scale)), OutputFormat: dara.String("png"), OutputQuality: dara.Int64(95)}, runtime)
	if err != nil || resp == nil || resp.Body == nil || resp.Body.Data == nil {
		return nil, aliFailure("PROCESS")
	}
	output, ok := aliyunImageResultURL(dara.StringValue(resp.Body.Data.Url))
	if !ok {
		return nil, aliFailure("OUTPUT_URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, output, nil)
	if err != nil {
		return nil, aliFailure("DOWNLOAD")
	}
	res, err := a.http.Do(req)
	if err != nil {
		return nil, aliFailure("DOWNLOAD")
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return nil, aliFailure("DOWNLOAD")
	}
	data, err := readBoundedResponse(res, a.maxBytes)
	if err != nil {
		return nil, aliFailure("DOWNLOAD")
	}
	return in.result(data, scale, a.maxBytes)
}

func aliFailure(stage string) error {
	return imageUpscaleError("ALIYUN_"+stage, http.StatusBadGateway, false, nil)
}

func (a *aliyunUpscaler) sdkConfig(ctx context.Context) (*api.Config, error) {
	if a.load == nil {
		return nil, aliFailure("CREDENTIAL_UNAVAILABLE")
	}
	raw, err := a.load(ctx)
	if err != nil {
		return nil, aliFailure("CREDENTIAL_UNAVAILABLE")
	}
	defer clearBytes(raw)
	var fields map[string]string
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 {
		return nil, aliFailure("CREDENTIAL_UNAVAILABLE")
	}
	id, secret := fields["access_key_id"], fields["access_key_secret"]
	if id == "" || secret == "" || strings.ContainsAny(id, " \r\n\t") || strings.ContainsAny(secret, " \r\n\t") {
		return nil, aliFailure("CREDENTIAL_UNAVAILABLE")
	}
	return &api.Config{AccessKeyId: &id, AccessKeySecret: &secret, RegionId: dara.String("cn-shanghai"), Endpoint: dara.String("openplatform.aliyuncs.com"), Protocol: dara.String("HTTPS"), HttpClient: aliyunRPCTransport{a.http}}, nil
}

type aliyunRPCTransport struct{ client *http.Client }

func (t aliyunRPCTransport) Call(req *http.Request, _ *http.Transport) (*http.Response, error) {
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, errors.New("aliyun request failed")
	}
	b, err := readBoundedResponse(resp, 64<<10)
	if err != nil {
		return nil, errors.New("aliyun response invalid")
	}
	resp.Body = io.NopCloser(bytes.NewReader(b))
	return resp, nil
}

func (a *aliyunUpscaler) stage(ctx context.Context, cfg *api.Config, runtime *dara.RuntimeOptions, data []byte) (string, error) {
	client, err := openapi.NewClient(cfg)
	if err != nil {
		return "", aliFailure("CLIENT")
	}
	params := &api.Params{Action: dara.String("AuthorizeFileUpload"), Version: dara.String("2019-12-19"), Protocol: dara.String("HTTPS"), Pathname: dara.String("/"), Method: dara.String("GET"), AuthType: dara.String("AK"), Style: dara.String("RPC"), ReqBodyType: dara.String("formData"), BodyType: dara.String("json")}
	auth, err := client.CallApiWithCtx(ctx, params, &api.OpenApiRequest{Query: map[string]*string{"Product": dara.String("imageenhan"), "RegionId": cfg.RegionId}}, runtime)
	if err != nil {
		return "", aliFailure("UPLOAD_AUTH")
	}
	fields, _ := auth["body"].(map[string]any)
	get := func(key string) string { v, _ := fields[key].(string); return v }
	bucket, key := get("Bucket"), get("ObjectKey")
	if !aliyunBucketName.MatchString(bucket) || get("Endpoint") != aliyunOSSRegion || key == "" || path.Clean(key) != key || key == "." || key == ".." || strings.HasPrefix(key, "../") || strings.HasPrefix(key, "/") || strings.ContainsAny(key, "\r\n\\") {
		return "", aliFailure("UPLOAD_POLICY")
	}
	target := url.URL{Scheme: "https", Host: bucket + "." + aliyunOSSRegion}
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for name, value := range map[string]string{"OSSAccessKeyId": get("AccessKeyId"), "policy": get("EncodedPolicy"), "Signature": get("Signature"), "key": key, "success_action_status": "201"} {
		if value == "" || w.WriteField(name, value) != nil {
			return "", aliFailure("UPLOAD_POLICY")
		}
	}
	if err := finishAliyunForm(w, data); err != nil {
		return "", aliFailure("UPLOAD")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String()+"/", &b)
	if err != nil {
		return "", aliFailure("UPLOAD")
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := a.http.Do(req)
	if err != nil {
		return "", aliFailure("UPLOAD")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		return "", aliFailure("UPLOAD")
	}
	target.Path = "/" + key
	return target.String(), nil
}

func finishAliyunForm(w *multipart.Writer, data []byte) error {
	part, err := w.CreateFormFile("file", "source"+upscaleImageExtension(data))
	if err != nil {
		return err
	}
	if _, err = part.Write(data); err != nil {
		return err
	}
	return w.Close()
}

func aliyunImageResultURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || len(u.Path) <= 1 || u.Opaque != "" {
		return "", false
	}
	switch u.Hostname() {
	case "ivpd-cn-shanghai." + aliyunOSSRegion, "vibktprfx-prod-prod-aic-vc-cn-shanghai." + aliyunOSSRegion:
		if u.Scheme == "http" && u.Host == u.Hostname() {
			u.Scheme = "https"
		}
	case "viapi-test." + aliyunOSSRegion: // Existing VSO HTTPS-only output host.
	default:
		return "", false
	}
	if u.Scheme != "https" || (u.Host != u.Hostname() && u.Host != u.Hostname()+":443") {
		return "", false
	}
	return u.String(), true
}
