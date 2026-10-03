package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/slack-go/slack"
)

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"slack": providerserver.NewProtocol6WithError(New("test")()),
}

// testProviderConfig is a minimal provider config that passes schema validation.
// The testAPIClient override bypasses all authentication, so this value is unused.
const testProviderConfig = `
provider "slack" {
  token = "xoxb-unused"
}
`

func setupTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// setupTestClient sets the package-level testAPIClient with a Slack client pointing
// at the test server. Must be called before running terraform-plugin-testing steps.
func setupTestClient(t *testing.T, server *httptest.Server) {
	t.Helper()
	testAPIClient = &apiClient{slack: slack.New(testToken, slack.OptionAPIURL(server.URL+"/"), slack.OptionHTTPClient(server.Client()))}
	t.Cleanup(func() { testAPIClient = nil })
}

func TestFakeRejectsMissingToken(t *testing.T) {
	server := setupTestServer(t, newFakeSlack())
	client := slack.New("xoxb-wrong", slack.OptionAPIURL(server.URL+"/"), slack.OptionHTTPClient(server.Client()))

	_, err := client.GetConversationInfoContext(t.Context(), &slack.GetConversationInfoInput{ChannelID: "C0000000001"})
	if !hasSlackError(err, "invalid_auth") {
		t.Fatalf("expected a request with the wrong token to fail with invalid_auth, got: %v", err)
	}
}

func TestConversationGoneErrorsAreNotFound(t *testing.T) {
	server := setupTestServer(t, newFakeSlack())
	setupTestClient(t, server)

	ctx := t.Context()

	_, err := testAPIClient.slack.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: "C_MISSING"})
	if err == nil || !isNotFound(err) {
		t.Fatalf("expected GetConversationInfo for a missing conversation to return a not-found error, got: %v", err)
	}

	err = testAPIClient.slack.ArchiveConversationContext(ctx, "C_MISSING")
	if err == nil || !isNotFound(err) {
		t.Fatalf("expected ArchiveConversation for a missing conversation to return a not-found error, got: %v", err)
	}
}
