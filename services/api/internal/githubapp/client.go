package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrUnauthorized = errors.New("github authorization failed")
	ErrForbidden    = errors.New("github request forbidden")
	ErrNotFound     = errors.New("github installation not found")
	ErrRateLimited  = errors.New("github rate limit exceeded")
	ErrUnavailable  = errors.New("github temporarily unavailable")
)

type Repository struct {
	ID            int64
	Owner         string
	Name          string
	FullName      string
	DefaultBranch string
	Private       bool
	Archived      bool
	Disabled      bool
	HTMLURL       string
	UpdatedAt     time.Time
}

type Installation struct {
	ID                  int64
	AccountID           int64
	AccountLogin        string
	AccountType         string
	RepositorySelection string
}

type Client interface {
	ExchangeUserCode(context.Context, string) (string, error)
	GetUserInstallation(context.Context, string, int64) (Installation, error)
	GetInstallation(context.Context, int64) (Installation, error)
	ListRepositories(context.Context, int64) ([]Repository, error)
	DeleteInstallation(context.Context, int64) error
}

type HTTPClient struct {
	appID        string
	clientID     string
	clientSecret string
	privateKey   *rsa.PrivateKey
	baseURL      string
	oauthURL     string
	http         *http.Client
	now          func() time.Time
}

func NewHTTPClient(appID, clientID, clientSecret, privateKeyPEM, baseURL, oauthURL string, client *http.Client) (*HTTPClient, error) {
	key, err := parsePrivateKey([]byte(privateKeyPEM))
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &HTTPClient{appID: appID, clientID: clientID, clientSecret: clientSecret, privateKey: key, baseURL: strings.TrimRight(baseURL, "/"), oauthURL: strings.TrimRight(oauthURL, "/"), http: client, now: time.Now}, nil
}

func (c *HTTPClient) ExchangeUserCode(ctx context.Context, code string) (string, error) {
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	form := url.Values{"client_id": {c.clientID}, "client_secret": {c.clientSecret}, "code": {code}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oauthURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := c.do(req, &payload); err != nil {
		return "", err
	}
	if payload.AccessToken == "" {
		return "", ErrUnauthorized
	}
	return payload.AccessToken, nil
}

func (c *HTTPClient) GetUserInstallation(ctx context.Context, token string, id int64) (Installation, error) {
	for page := 1; ; page++ {
		var payload struct {
			Installations []struct {
				ID      int64 `json:"id"`
				Account struct {
					ID    int64  `json:"id"`
					Login string `json:"login"`
					Type  string `json:"type"`
				} `json:"account"`
				RepositorySelection string `json:"repository_selection"`
			} `json:"installations"`
		}
		path := fmt.Sprintf("/user/installations?per_page=100&page=%d", page)
		if err := c.request(ctx, http.MethodGet, path, token, nil, &payload); err != nil {
			return Installation{}, err
		}
		for _, candidate := range payload.Installations {
			if candidate.ID == id {
				return Installation{ID: candidate.ID, AccountID: candidate.Account.ID, AccountLogin: candidate.Account.Login, AccountType: candidate.Account.Type, RepositorySelection: candidate.RepositorySelection}, nil
			}
		}
		if len(payload.Installations) < 100 {
			return Installation{}, ErrForbidden
		}
	}
}

func (c *HTTPClient) GetInstallation(ctx context.Context, id int64) (Installation, error) {
	token, err := c.appJWT()
	if err != nil {
		return Installation{}, err
	}
	return c.getInstallation(ctx, fmt.Sprintf("/app/installations/%d", id), token)
}

func (c *HTTPClient) getInstallation(ctx context.Context, path, token string) (Installation, error) {
	var payload struct {
		ID      int64 `json:"id"`
		Account struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
		RepositorySelection string `json:"repository_selection"`
	}
	if err := c.request(ctx, http.MethodGet, path, token, nil, &payload); err != nil {
		return Installation{}, err
	}
	return Installation{ID: payload.ID, AccountID: payload.Account.ID, AccountLogin: payload.Account.Login, AccountType: payload.Account.Type, RepositorySelection: payload.RepositorySelection}, nil
}

func (c *HTTPClient) DeleteInstallation(ctx context.Context, id int64) error {
	return c.appRequest(ctx, http.MethodDelete, fmt.Sprintf("/app/installations/%d", id), nil, nil)
}

func (c *HTTPClient) ListRepositories(ctx context.Context, installationID int64) ([]Repository, error) {
	token, err := c.installationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	// The token exists only in this call stack and is discarded after this request sequence.
	var result []Repository
	for page := 1; ; page++ {
		var payload struct {
			Repositories []struct {
				ID            int64     `json:"id"`
				Name          string    `json:"name"`
				FullName      string    `json:"full_name"`
				DefaultBranch string    `json:"default_branch"`
				Private       bool      `json:"private"`
				Archived      bool      `json:"archived"`
				Disabled      bool      `json:"disabled"`
				HTMLURL       string    `json:"html_url"`
				UpdatedAt     time.Time `json:"updated_at"`
				Owner         struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repositories"`
		}
		path := fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page)
		if err := c.request(ctx, http.MethodGet, path, token, nil, &payload); err != nil {
			return nil, err
		}
		for _, repo := range payload.Repositories {
			result = append(result, Repository{ID: repo.ID, Owner: repo.Owner.Login, Name: repo.Name, FullName: repo.FullName, DefaultBranch: repo.DefaultBranch, Private: repo.Private, Archived: repo.Archived, Disabled: repo.Disabled, HTMLURL: repo.HTMLURL, UpdatedAt: repo.UpdatedAt})
		}
		if len(payload.Repositories) < 100 {
			break
		}
	}
	return result, nil
}

func (c *HTTPClient) installationToken(ctx context.Context, id int64) (string, error) {
	var payload struct {
		Token string `json:"token"`
	}
	body := map[string]any{"permissions": map[string]string{"metadata": "read"}}
	if err := c.appRequest(ctx, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", id), body, &payload); err != nil {
		return "", err
	}
	if payload.Token == "" {
		return "", errors.New("github returned an empty installation token")
	}
	return payload.Token, nil
}

func (c *HTTPClient) appRequest(ctx context.Context, method, path string, body, output any) error {
	token, err := c.appJWT()
	if err != nil {
		return err
	}
	return c.request(ctx, method, path, token, body, output)
}

func (c *HTTPClient) request(ctx context.Context, method, path, token string, body, output any) error {
	return c.requestURL(ctx, method, c.baseURL+path, token, body, output)
}

func (c *HTTPClient) requestURL(ctx context.Context, method, endpoint, token string, body, output any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, output)
}

func (c *HTTPClient) do(req *http.Request, output any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return classify(resp.StatusCode, resp.Header)
	}
	if output == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode github response: %w", err)
	}
	return nil
}

func classify(status int, header http.Header) error {
	switch status {
	case 401:
		return ErrUnauthorized
	case 403:
		if header.Get("X-RateLimit-Remaining") == "0" {
			return fmt.Errorf("%w; reset=%s", ErrRateLimited, header.Get("X-RateLimit-Reset"))
		}
		return ErrForbidden
	case 404:
		return ErrNotFound
	case 429:
		return ErrRateLimited
	default:
		if status >= 500 {
			return ErrUnavailable
		}
		return fmt.Errorf("github returned status %d", status)
	}
}

func (c *HTTPClient) appJWT() (string, error) {
	now := c.now().UTC()
	header := encodeJSON(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims := encodeJSON(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": c.appID})
	input := header + "." + claims
	hash := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", err
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func encodeJSON(value any) string {
	data, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(data)
}
func parsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("GitHub private key must be PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	value, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("GitHub private key is invalid")
	}
	key, ok := value.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("GitHub private key must be RSA")
	}
	return key, nil
}
