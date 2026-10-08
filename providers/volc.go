package providers

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const volcAlgorithm = "HMAC-SHA256"

// signVolcengine applies the official Volcengine OpenAPI V4 signature.
// Service cv and region cn-north-1 are the fixed values for visual.volcengineapi.com.
func signVolcengine(req *http.Request, body []byte, accessKey, secret, region, service string, now time.Time) error {
	if accessKey == "" || secret == "" {
		return fmt.Errorf("volcengine access key and secret required")
	}
	if req.URL == nil || req.URL.Host == "" {
		return fmt.Errorf("volcengine request host required")
	}
	if region == "" {
		region = "cn-north-1"
	}
	if service == "" {
		service = "cv"
	}
	xDate := now.UTC().Format("20060102T150405Z")
	shortDate := xDate[:8]
	payloadHash := sha256Hex(body)
	contentType := "application/json"
	host := req.URL.Host
	query := canonicalQuery(req.URL.Query())
	canonical := "POST\n/\n" + query + "\n" +
		"content-type:" + contentType + "\n" +
		"host:" + host + "\n" +
		"x-content-sha256:" + payloadHash + "\n" +
		"x-date:" + xDate + "\n\n" +
		"content-type;host;x-content-sha256;x-date\n" +
		payloadHash
	scope := shortDate + "/" + region + "/" + service + "/request"
	stringToSign := volcAlgorithm + "\n" + xDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	signingKey := hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256([]byte(secret), shortDate), region), service), "request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	req.Host = host
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", host)
	req.Header.Set("X-Date", xDate)
	req.Header.Set("X-Content-Sha256", payloadHash)
	req.Header.Set("Authorization", volcAlgorithm+" Credential="+accessKey+"/"+scope+", SignedHeaders=content-type;host;x-content-sha256;x-date, Signature="+signature)
	return nil
}

func canonicalQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		items := append([]string(nil), values[key]...)
		sort.Strings(items)
		for _, value := range items {
			parts = append(parts, rfc3986(key)+"="+rfc3986(value))
		}
	}
	return strings.Join(parts, "&")
}

func rfc3986(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}
