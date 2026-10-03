package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/slack-go/slack"
)

// appsClient calls the apps.manifest.* methods with the manifest as raw JSON.
// slack-go v0.29.0 decodes manifests into a fixed struct that drops fields it
// does not model, and its create response omits app_id and credentials.
type appsClient struct {
	httpClient *http.Client
	apiURL     string
	token      string
}

func newAppsClient(token string, httpClient *http.Client, apiURL string) *appsClient {
	return &appsClient{httpClient: httpClient, apiURL: apiURL, token: token}
}

type appCredentials struct {
	ClientID          string `json:"client_id"`
	ClientSecret      string `json:"client_secret"`
	VerificationToken string `json:"verification_token"`
	SigningSecret     string `json:"signing_secret"`
}

type appCreated struct {
	AppID             string         `json:"app_id"`
	Credentials       appCredentials `json:"credentials"`
	OAuthAuthorizeURL string         `json:"oauth_authorize_url"`
}

type manifestError struct {
	Message string `json:"message"`
	Pointer string `json:"pointer"`
}

type appsResponse struct {
	Ok     bool            `json:"ok"`
	Error  string          `json:"error"`
	Errors []manifestError `json:"errors"`
}

func (c *appsClient) createManifest(ctx context.Context, manifest string) (*appCreated, error) {
	out := &appCreated{}
	if err := c.call(ctx, "apps.manifest.create", url.Values{"manifest": {manifest}}, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *appsClient) updateManifest(ctx context.Context, appID, manifest string) error {
	return c.call(ctx, "apps.manifest.update", url.Values{"app_id": {appID}, "manifest": {manifest}}, nil)
}

func (c *appsClient) validateManifest(ctx context.Context, appID, manifest string) error {
	values := url.Values{"manifest": {manifest}}
	if appID != "" {
		values.Set("app_id", appID)
	}
	return c.call(ctx, "apps.manifest.validate", values, nil)
}

func (c *appsClient) exportManifest(ctx context.Context, appID string) (json.RawMessage, error) {
	out := &struct {
		Manifest json.RawMessage `json:"manifest"`
	}{}
	if err := c.call(ctx, "apps.manifest.export", url.Values{"app_id": {appID}}, out); err != nil {
		return nil, err
	}
	return out.Manifest, nil
}

func (c *appsClient) deleteManifest(ctx context.Context, appID string) error {
	return c.call(ctx, "apps.manifest.delete", url.Values{"app_id": {appID}}, nil)
}

func (c *appsClient) call(ctx context.Context, method string, values url.Values, out any) error {
	values.Set("token", c.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+method, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = httpResp.Body.Close() }()
	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d", method, httpResp.StatusCode)
	}

	var raw json.RawMessage
	if err := json.NewDecoder(httpResp.Body).Decode(&raw); err != nil {
		return fmt.Errorf("decoding %s response: %w", method, err)
	}
	var envelope appsResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decoding %s response: %w", method, err)
	}
	if !envelope.Ok {
		return manifestSlackError(envelope)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func manifestSlackError(envelope appsResponse) slack.SlackErrorResponse {
	messages := make([]string, 0, len(envelope.Errors))
	for _, e := range envelope.Errors {
		messages = append(messages, fmt.Sprintf("%s: %s", e.Pointer, e.Message))
	}
	return slack.SlackErrorResponse{Err: envelope.Error, ResponseMetadata: slack.ResponseMetadata{Messages: messages}}
}
