package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	gcsSignedURLHost       = "storage.googleapis.com"
	gcsV4Algorithm         = "GOOG4-RSA-SHA256"
	gcsV4RequestTerminator = "goog4_request"
)

type BatchImageSignedResultSource struct {
	ID  string
	URL string
}

type BatchImageSignedResultSourceProvider interface {
	SignedResultSources(ctx context.Context, job *BatchImageJob, account *Account, ttl time.Duration) ([]BatchImageSignedResultSource, error)
}

func (p *VertexBatchImageProvider) SignedResultSources(ctx context.Context, job *BatchImageJob, account *Account, ttl time.Duration) ([]BatchImageSignedResultSource, error) {
	if err := p.validateAccount(account); err != nil {
		return nil, err
	}
	outputRef := batchImageProviderOutputRef(job)
	if outputRef == "" && job != nil && job.GCSOutputURI != nil {
		outputRef = strings.TrimSpace(*job.GCSOutputURI)
	}
	if outputRef == "" || !p.isSafeManagedOutput(job, outputRef) {
		return nil, ErrBatchImageProviderUnsafeCleanupPath
	}
	accessToken, err := p.accessToken(ctx, account)
	if err != nil {
		return nil, mapVertexClientError(err)
	}
	objects, err := p.objectStore.ListJSONLObjects(ctx, accessToken, outputRef)
	if err != nil {
		return nil, vertexProviderError("VERTEX_GCS_LIST_FAILED", "Vertex managed GCS list failed", nil)
	}
	sort.Strings(objects)
	if len(objects) == 0 {
		return nil, vertexProviderError("VERTEX_RESULT_OBJECTS_MISSING", "Vertex result objects are missing", nil)
	}
	key, err := parseVertexServiceAccountKey(account)
	if err != nil {
		return nil, ErrBatchImageProviderMissingServiceAccount
	}
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(key.PrivateKey))
	if err != nil {
		return nil, ErrBatchImageProviderMissingServiceAccount
	}
	result := make([]BatchImageSignedResultSource, 0, len(objects))
	for index, objectURI := range objects {
		if !p.isSafeManagedOutput(job, objectURI) || !strings.HasSuffix(strings.ToLower(objectURI), ".jsonl") {
			return nil, ErrBatchImageProviderUnsafeCleanupPath
		}
		bucket, object, err := parseGCSObjectURI(objectURI)
		if err != nil || bucket != p.opts.ManagedGCSBucket {
			return nil, ErrBatchImageProviderUnsafeCleanupPath
		}
		signed, err := signGCSV4GetURL(bucket, object, key.ClientEmail, privateKey, time.Now().UTC(), ttl)
		if err != nil {
			return nil, ErrBatchImageDeliveryFailed
		}
		result = append(result, BatchImageSignedResultSource{
			ID:  fmt.Sprintf("result-%03d.jsonl", index+1),
			URL: signed,
		})
	}
	return result, nil
}

func parseGCSObjectURI(raw string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "gs" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", errors.New("invalid GCS object URI")
	}
	object := strings.TrimPrefix(parsed.EscapedPath(), "/")
	decoded, err := url.PathUnescape(object)
	if err != nil || decoded == "" {
		return "", "", errors.New("invalid GCS object URI")
	}
	return parsed.Host, decoded, nil
}

func gcsCanonicalObjectPath(bucket, object string) string {
	segments := append([]string{bucket}, strings.Split(object, "/")...)
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return "/" + strings.Join(segments, "/")
}

func signGCSV4GetURL(bucket, object, clientEmail string, privateKey *rsa.PrivateKey, now time.Time, ttl time.Duration) (string, error) {
	bucket = strings.TrimSpace(bucket)
	object = strings.TrimLeft(strings.TrimSpace(object), "/")
	clientEmail = strings.TrimSpace(clientEmail)
	if bucket == "" || object == "" || clientEmail == "" || privateKey == nil {
		return "", errors.New("GCS signing parameters are incomplete")
	}
	expires := int64(ttl / time.Second)
	if expires <= 0 || expires > 7*24*60*60 {
		return "", errors.New("GCS signed URL expiry is invalid")
	}
	now = now.UTC()
	date := now.Format("20060102")
	timestamp := now.Format("20060102T150405Z")
	scope := date + "/auto/storage/" + gcsV4RequestTerminator
	query := url.Values{}
	query.Set("X-Goog-Algorithm", gcsV4Algorithm)
	query.Set("X-Goog-Credential", clientEmail+"/"+scope)
	query.Set("X-Goog-Date", timestamp)
	query.Set("X-Goog-Expires", strconv.FormatInt(expires, 10))
	query.Set("X-Goog-SignedHeaders", "host")
	canonicalPath := gcsCanonicalObjectPath(bucket, object)
	canonicalRequest := strings.Join([]string{
		"GET",
		canonicalPath,
		query.Encode(),
		"host:" + gcsSignedURLHost + "\n",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	canonicalHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		gcsV4Algorithm,
		timestamp,
		scope,
		hex.EncodeToString(canonicalHash[:]),
	}, "\n")
	digest := sha256.Sum256([]byte(stringToSign))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", errors.New("sign GCS URL failed")
	}
	query.Set("X-Goog-Signature", hex.EncodeToString(signature))
	return "https://" + gcsSignedURLHost + canonicalPath + "?" + query.Encode(), nil
}
