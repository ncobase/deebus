package providers

import (
	"context"
	"net/http"
)

// Credentials contains the effective request credentials for a provider call.
type Credentials struct {
	APIKey       string
	BearerToken  string
	AccessKey    string
	Secret       string
	Headers      map[string]string
	Organization string
	Project      string
	UserProject  string
}

// CredentialProvider resolves request credentials at call time.
// Implementations may return static values, short-lived bearer tokens, or
// proxy-specific headers.
type CredentialProvider interface {
	Credentials(ctx context.Context) (Credentials, error)
}

func (c Config) credentials(ctx context.Context) (Credentials, error) {
	creds := Credentials{
		APIKey:       c.APIKey,
		BearerToken:  c.BearerToken,
		AccessKey:    c.AccessKey,
		Secret:       c.Secret,
		Headers:      cloneHeaders(c.Headers),
		Organization: c.Organization,
		Project:      c.Project,
		UserProject:  c.UserProject,
	}
	if c.CredentialProvider != nil {
		dynamic, err := c.CredentialProvider.Credentials(ctx)
		if err != nil {
			return Credentials{}, err
		}
		mergeCredentials(&creds, dynamic)
	}
	if err := validateCredentialHeaders(creds); err != nil {
		return Credentials{}, err
	}
	return creds, nil
}

func validateCredentialHeaders(creds Credentials) error {
	return ValidateHeaderSafety(creds.Headers, map[string]string{
		"apiKey":       creds.APIKey,
		"bearerToken":  creds.BearerToken,
		"accessKey":    creds.AccessKey,
		"secret":       creds.Secret,
		"organization": creds.Organization,
		"project":      creds.Project,
		"userProject":  creds.UserProject,
	})
}

func mergeCredentials(dst *Credentials, src Credentials) {
	if src.APIKey != "" {
		dst.APIKey = src.APIKey
	}
	if src.BearerToken != "" {
		dst.BearerToken = src.BearerToken
	}
	if src.AccessKey != "" {
		dst.AccessKey = src.AccessKey
	}
	if src.Secret != "" {
		dst.Secret = src.Secret
	}
	if src.Organization != "" {
		dst.Organization = src.Organization
	}
	if src.Project != "" {
		dst.Project = src.Project
	}
	if src.UserProject != "" {
		dst.UserProject = src.UserProject
	}
	if len(src.Headers) == 0 {
		return
	}
	if dst.Headers == nil {
		dst.Headers = make(map[string]string, len(src.Headers))
	}
	for k, v := range src.Headers {
		dst.Headers[k] = v
	}
}

func cloneHeaders(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func applyHeaders(r *http.Request, headers map[string]string) {
	for k, v := range headers {
		r.Header.Set(k, v)
	}
}
