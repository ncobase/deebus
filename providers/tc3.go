package providers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const tc3Algorithm = "TC3-HMAC-SHA256"

// signTC3 applies the official Tencent Cloud TC3-HMAC-SHA256 signature.
// The secret key is used only to derive the signature. Signed headers are
// content-type, host, and x-tc-action, matching the Tencent Cloud v3 example.
func signTC3(req *http.Request, body []byte, secretID, secretKey, service, action, version, region string, now time.Time) error {
	if secretID == "" || secretKey == "" {
		return fmt.Errorf("tencent secret id and secret key required")
	}
	if req.URL == nil || req.URL.Host == "" {
		return fmt.Errorf("tencent request host required")
	}
	timestamp := now.Unix()
	date := now.UTC().Format("2006-01-02")
	host := req.URL.Host
	contentType := "application/json; charset=utf-8"
	canonicalHeaders := "content-type:" + contentType + "\n" +
		"host:" + host + "\n" +
		"x-tc-action:" + strings.ToLower(action) + "\n"
	signedHeaders := "content-type;host;x-tc-action"
	canonical := strings.ToUpper(req.Method) + "\n/\n\n" +
		canonicalHeaders + "\n" +
		signedHeaders + "\n" +
		sha256Hex(body)
	scope := date + "/" + service + "/tc3_request"
	stringToSign := tc3Algorithm + "\n" +
		fmt.Sprintf("%d", timestamp) + "\n" +
		scope + "\n" +
		sha256Hex([]byte(canonical))
	secretDate := hmacSHA256([]byte("TC3"+secretKey), date)
	secretService := hmacSHA256(secretDate, service)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))
	req.Host = host
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", host)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", version)
	req.Header.Set("X-TC-Timestamp", fmt.Sprintf("%d", timestamp))
	if region != "" {
		req.Header.Set("X-TC-Region", region)
	}
	req.Header.Set("Authorization", tc3Algorithm+" Credential="+secretID+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
	return nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
