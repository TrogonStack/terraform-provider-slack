package provider

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const testIconAppID = "A0000000001"

func writeTestIcon(t *testing.T, name string, pixels int, shade uint8) string {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, pixels, pixels))
	for i := range img.Pix {
		img.Pix[i] = shade
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(file, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func fakeWithApp() *fakeSlack {
	fake := newFakeSlack()
	fake.apps[testIconAppID] = map[string]any{}
	return fake
}

func appIconFileConfig(appID, file string) string {
	return testProviderConfig + fmt.Sprintf(`
resource "slack_app_icon" "test" {
  app_id = %q
  file   = %q
  sha256 = filesha256(%q)
}
`, appID, file, file)
}

func appIconURLConfig(appID, url string) string {
	return testProviderConfig + fmt.Sprintf(`
resource "slack_app_icon" "test" {
  app_id = %q
  url    = %q
}
`, appID, url)
}

func checkIconUploads(fake *fakeSlack, appID string, want int) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if got := len(fake.icons[appID]); got != want {
			return fmt.Errorf("expected %d apps.icon.set calls for %s, got %d", want, appID, got)
		}
		return nil
	}
}

func TestAccAppIcon_File(t *testing.T) {
	fake := fakeWithApp()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	first := writeTestIcon(t, "first.png", 512, 10)
	second := writeTestIcon(t, "second.png", 600, 200)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkIconUploads(fake, testIconAppID, 2),
		Steps: []resource.TestStep{
			{
				Config: appIconFileConfig(testIconAppID, first),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack_app_icon.test", "id", testIconAppID),
					resource.TestCheckResourceAttr("slack_app_icon.test", "app_id", testIconAppID),
					checkIconUploads(fake, testIconAppID, 1),
					func(_ *terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						icon := fake.icons[testIconAppID][0]
						want, _ := os.ReadFile(first)
						if icon.Filename != "first.png" || !bytes.Equal(icon.Content, want) {
							return fmt.Errorf("expected Slack to receive first.png byte for byte, got %q with %d bytes", icon.Filename, len(icon.Content))
						}
						return nil
					},
				),
			},
			{
				Config:             appIconFileConfig(testIconAppID, first),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
			{
				Config: appIconFileConfig(testIconAppID, second),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack_app_icon.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: checkIconUploads(fake, testIconAppID, 2),
			},
		},
	})
}

func TestAccAppIcon_URL(t *testing.T) {
	fake := fakeWithApp()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkIconUploads(fake, testIconAppID, 2),
		Steps: []resource.TestStep{
			{
				Config: appIconURLConfig(testIconAppID, "https://example.com/icon.png"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack_app_icon.test", "id", testIconAppID),
					func(_ *terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						if got := fake.icons[testIconAppID][0].URL; got != "https://example.com/icon.png" {
							return fmt.Errorf("expected Slack to receive the url, got %q", got)
						}
						return nil
					},
				),
			},
			{
				Config: appIconURLConfig(testIconAppID, "https://example.com/other.png"),
				Check:  checkIconUploads(fake, testIconAppID, 2),
			},
		},
	})
}

func TestAccAppIcon_ReplacesWhenAppChanges(t *testing.T) {
	fake := fakeWithApp()
	fake.apps["A0000000002"] = map[string]any{}
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	icon := writeTestIcon(t, "icon.png", 512, 10)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: appIconFileConfig(testIconAppID, icon)},
			{
				Config: appIconFileConfig("A0000000002", icon),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack_app_icon.test", plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: checkIconUploads(fake, "A0000000002", 1),
			},
		},
	})
}

func TestAccAppIcon_AppNotFound(t *testing.T) {
	setupTestClient(t, setupTestServer(t, newFakeSlack()))
	icon := writeTestIcon(t, "icon.png", 512, 10)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      appIconFileConfig(testIconAppID, icon),
				ExpectError: regexp.MustCompile(`Unable to set app icon: app_not_found`),
			},
		},
	})
}

func TestAccAppIcon_Validation(t *testing.T) {
	setupTestClient(t, setupTestServer(t, fakeWithApp()))
	small := writeTestIcon(t, "small.png", 256, 10)
	large := writeTestIcon(t, "large.png", 2001, 10)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      appIconFileConfig(testIconAppID, small),
				ExpectError: regexp.MustCompile(`256x256 pixels, but Slack needs 512 to 2000`),
			},
			{
				Config:      appIconFileConfig(testIconAppID, large),
				ExpectError: regexp.MustCompile(`2001x2001 pixels, but Slack needs 512 to 2000`),
			},
			{
				Config: testProviderConfig + `
resource "slack_app_icon" "test" {
  app_id = "A0000000001"
  file   = "icon.png"
  url    = "https://example.com/icon.png"
  sha256 = "abc"
}
`,
				ExpectError: regexp.MustCompile(`(?s)one \(and only one\)`),
			},
			{
				Config: testProviderConfig + `
resource "slack_app_icon" "test" {
  app_id = "A0000000001"
  file   = "icon.png"
}
`,
				ExpectError: regexp.MustCompile(`(?s)sha256`),
			},
			{
				Config: testProviderConfig + `
resource "slack_app_icon" "test" {
  app_id = "A0000000001"
}
`,
				ExpectError: regexp.MustCompile(`(?s)No attribute specified when one \(and only one\)`),
			},
		},
	})
}

func TestAccAppIcon_MissingAppConfigurationToken(t *testing.T) {
	setupTestClient(t, setupTestServer(t, fakeWithApp()))
	testAPIClient.apps = nil
	icon := writeTestIcon(t, "icon.png", 512, 10)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      appIconFileConfig(testIconAppID, icon),
				ExpectError: regexp.MustCompile(`slack_app_icon needs app_configuration_token`),
			},
		},
	})
}

func TestAppsClientSetIconRetriesRateLimitedUpload(t *testing.T) {
	var attempts atomic.Int32
	var lastBody int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			writeSlackError(w, "invalid_form_data")
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeSlackError(w, "invalid_form_data")
			return
		}
		content := make([]byte, 64)
		lastBody, _ = file.Read(content)
		writeSlackOK(w, nil)
	}))
	t.Cleanup(server.Close)

	client := newAppsClient(testConfigToken, newRetryableClient(), server.URL+"/")
	if err := client.setIconFromFile(t.Context(), testIconAppID, "icon.png", []byte("image bytes")); err != nil {
		t.Fatalf("expected the upload to succeed after a 429, got: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
	if lastBody != len("image bytes") {
		t.Fatalf("expected the retried upload to carry the whole file, got %d bytes", lastBody)
	}
}
