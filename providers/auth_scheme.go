package providers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// UsesAccessKey reports whether a provider type authenticates with an access
// key and secret instead of an API key. The signature algorithm is selected
// by the provider type. These fields are not a universal signing scheme.
func UsesAccessKey(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "kling", "hunyuan", "jimeng":
		return true
	default:
		return false
	}
}

// ValidateAuth checks that a provider uses either an API key style credential
// or an access key and secret, and that the pair matches the provider type.
func ValidateAuth(providerType, apiKey, bearer, accessKey, secret string, headers map[string]string, dynamic bool) error {
	hasKey := apiKey != "" || bearer != "" || len(headers) > 0 || dynamic
	hasAccess := accessKey != "" || secret != ""
	if hasAccess && (accessKey == "" || secret == "") {
		return fmt.Errorf("accessKey and secret must both be set")
	}
	if hasKey && hasAccess {
		return fmt.Errorf("use apiKey or accessKey and secret, not both")
	}
	if UsesAccessKey(providerType) {
		if !hasAccess {
			return fmt.Errorf("accessKey and secret required")
		}
		return nil
	}
	if hasAccess {
		return fmt.Errorf("accessKey and secret are not used by this provider")
	}
	if providerType != "ollama" && !hasKey {
		return fmt.Errorf("apiKey, bearerToken, credentialProvider, or headers required")
	}
	return nil
}

type klingClaims struct {
	Issuer    string `json:"iss"`
	ExpiresAt int64  `json:"exp"`
	NotBefore int64  `json:"nbf"`
}

// KlingToken builds the HS256 JWT used by the official Kling API.
// The access key is the issuer. The secret signs the token and is not sent.
func KlingToken(accessKey, secret string, now time.Time) (string, error) {
	if accessKey == "" || secret == "" {
		return "", fmt.Errorf("kling accessKey and secret required")
	}
	header, err := jwtPart([]byte(`{"alg":"HS256","typ":"JWT"}`))
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(klingClaims{
		Issuer:    accessKey,
		ExpiresAt: now.Add(30 * time.Minute).Unix(),
		NotBefore: now.Add(-5 * time.Second).Unix(),
	})
	if err != nil {
		return "", err
	}
	encodedPayload, err := jwtPart(payload)
	if err != nil {
		return "", err
	}
	signingInput := header + "." + encodedPayload
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signature, nil
}

func jwtPart(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("empty jwt part")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
