package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultBaseURL    = "https://api.obsidian.md"
	defaultHTTPTimeout = 30 * time.Second
)

// Client is an HTTP client for the Obsidian Sync REST API.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient returns a Client with sensible defaults.
func NewClient() *Client {
	return &Client{
		BaseURL: defaultBaseURL,
		HTTPClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// SigninRequest is the request body for POST /user/signin.
type SigninRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	MFA      string `json:"mfa,omitempty"`
}

// SigninResponse is the response from POST /user/signin.
type SigninResponse struct {
	Token string `json:"token"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// Vault represents a vault returned by the list endpoint.
type Vault struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Password          string `json:"password"`
	Salt              string `json:"salt"`
	Host              string `json:"host"`
	EncryptionVersion int    `json:"encryption_version"`
}

// ListVaultsRequest is the request body for POST /vault/list.
type ListVaultsRequest struct {
	Token                        string `json:"token"`
	SupportedEncryptionVersion   int    `json:"supported_encryption_version"`
}

// ListVaultsResponse is the response from POST /vault/list.
type ListVaultsResponse struct {
	Vaults []Vault `json:"vaults"`
	Shared []Vault `json:"shared"`
}

// VaultAccessRequest is the request body for POST /vault/access.
type VaultAccessRequest struct {
	Token             string `json:"token"`
	VaultUID          string `json:"vault_uid"`
	KeyHash           string `json:"keyhash"`
	Host              string `json:"host"`
	EncryptionVersion int    `json:"encryption_version"`
}

// VaultAccessResponse is the response from POST /vault/access.
type VaultAccessResponse struct {
	Host    string `json:"host"`
	Allowed bool   `json:"allowed"`
}

// APIError represents an error response from the Obsidian API.
type APIError struct {
	StatusCode int
	Message    string `json:"error"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("api: %s (HTTP %d)", e.Message, e.StatusCode)
	}
	return fmt.Sprintf("api: HTTP %d", e.StatusCode)
}

// Signin authenticates with Obsidian using email, password, and optional MFA code.
func (c *Client) Signin(ctx context.Context, email, password, mfa string) (*SigninResponse, error) {
	req := SigninRequest{
		Email:    email,
		Password: password,
		MFA:      mfa,
	}
	var resp SigninResponse
	if err := c.post(ctx, "/user/signin", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListVaults returns the user's own and shared vaults.
func (c *Client) ListVaults(ctx context.Context, token string) (*ListVaultsResponse, error) {
	req := ListVaultsRequest{
		Token:                      token,
		SupportedEncryptionVersion: 3,
	}
	var resp ListVaultsResponse
	if err := c.post(ctx, "/vault/list", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// VaultAccess validates keyhash and requests WebSocket access to a vault,
// returning the sync host. For encrypted vaults the response contains the host;
// for unencrypted vaults the caller-provided host is returned.
func (c *Client) VaultAccess(ctx context.Context, token, vaultUID, keyHash, host string, encVer int) (string, error) {
	req := VaultAccessRequest{
		Token:             token,
		VaultUID:          vaultUID,
		KeyHash:           keyHash,
		Host:              host,
		EncryptionVersion: encVer,
	}
	var resp VaultAccessResponse
	if err := c.post(ctx, "/vault/access", req, &resp); err != nil {
		return "", err
	}
	if resp.Host != "" {
		return resp.Host, nil
	}
	return host, nil
}

// post sends a JSON POST request and decodes the response.
func (c *Client) post(ctx context.Context, path string, body, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("api: marshal request: %w", err)
	}

	url := c.BaseURL + path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("api: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Origin", "app://obsidian.md")

	httpResp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("api: %s: %w", path, err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("api: read response: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		apiErr := &APIError{StatusCode: httpResp.StatusCode}
		_ = json.Unmarshal(respBody, apiErr)
		return apiErr
	}

	// The Obsidian API may return HTTP 200 with an error in the body.
	var errCheck struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(respBody, &errCheck) == nil && errCheck.Error != "" {
		return &APIError{StatusCode: httpResp.StatusCode, Message: errCheck.Error}
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("api: decode response: %w", err)
		}
	}
	return nil
}
