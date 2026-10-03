package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/slack-go/slack"
)

const (
	testToken       = "xoxb-test"
	testConfigToken = "xoxe-test"
)

const fakeCreatedAt slack.JSONTime = 1700000000

type fakeSlack struct {
	mu            sync.Mutex
	nextChannelID int
	nextGroupID   int
	conversations map[string]*slack.Channel
	usergroups    map[string]*slack.UserGroup
	nextAppID     int
	apps          map[string]map[string]any
}

func newFakeSlack() *fakeSlack {
	return &fakeSlack{
		conversations: make(map[string]*slack.Channel),
		usergroups:    make(map[string]*slack.UserGroup),
		apps:          make(map[string]map[string]any),
	}
}

func (f *fakeSlack) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeSlackError(w, "invalid_form_data")
		return
	}
	method := strings.TrimPrefix(r.URL.Path, "/")
	wantToken := testToken
	if strings.HasPrefix(method, "apps.manifest.") {
		wantToken = testConfigToken
	}
	if r.PostForm.Get("token") != wantToken {
		writeSlackError(w, "invalid_auth")
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	form := r.PostForm
	switch method {
	case "apps.manifest.validate":
		if appID := form.Get("app_id"); appID != "" {
			if _, ok := f.apps[appID]; !ok {
				writeSlackError(w, "app_not_found")
				return
			}
		}
		if _, ok := parseFakeManifest(w, form.Get("manifest")); ok {
			writeSlackOK(w, nil)
		}
	case "apps.manifest.create":
		manifest, ok := parseFakeManifest(w, form.Get("manifest"))
		if !ok {
			return
		}
		f.nextAppID++
		id := fmt.Sprintf("A%010d", f.nextAppID)
		f.apps[id] = manifest
		writeSlackOK(w, map[string]any{
			"app_id": id,
			"credentials": map[string]any{
				"client_id":          "1111." + id,
				"client_secret":      "secret-" + id,
				"verification_token": "verify-" + id,
				"signing_secret":     "signing-" + id,
			},
			"oauth_authorize_url": "https://slack.com/oauth/v2/authorize?client_id=1111." + id,
		})
	case "apps.manifest.update":
		f.withApp(w, form, func(id string) {
			manifest, ok := parseFakeManifest(w, form.Get("manifest"))
			if !ok {
				return
			}
			f.apps[id] = manifest
			writeSlackOK(w, map[string]any{"app_id": id, "permissions_updated": false})
		})
	case "apps.manifest.export":
		f.withApp(w, form, func(id string) {
			writeSlackOK(w, map[string]any{"manifest": withSlackDefaults(f.apps[id])})
		})
	case "apps.manifest.delete":
		f.withApp(w, form, func(id string) {
			delete(f.apps, id)
			writeSlackOK(w, nil)
		})
	case "conversations.create":
		f.createConversation(w, form)
	case "conversations.info":
		f.withConversation(w, form, func(c *slack.Channel) { writeSlackOK(w, map[string]any{"channel": c}) })
	case "conversations.rename":
		f.withActiveConversation(w, form, func(c *slack.Channel) {
			if f.nameTaken(form.Get("name"), c.ID) {
				writeSlackError(w, "name_taken")
				return
			}
			c.Name = form.Get("name")
			writeSlackOK(w, map[string]any{"channel": c})
		})
	case "conversations.setTopic":
		f.withActiveConversation(w, form, func(c *slack.Channel) {
			c.Topic.Value = form.Get("topic")
			writeSlackOK(w, map[string]any{"channel": c})
		})
	case "conversations.setPurpose":
		f.withActiveConversation(w, form, func(c *slack.Channel) {
			c.Purpose.Value = form.Get("purpose")
			writeSlackOK(w, map[string]any{"channel": c})
		})
	case "conversations.archive":
		f.withConversation(w, form, func(c *slack.Channel) {
			if c.IsArchived {
				writeSlackError(w, "already_archived")
				return
			}
			c.IsArchived = true
			writeSlackOK(w, nil)
		})
	case "conversations.unarchive":
		f.withConversation(w, form, func(c *slack.Channel) {
			if !c.IsArchived {
				writeSlackError(w, "not_archived")
				return
			}
			c.IsArchived = false
			writeSlackOK(w, nil)
		})
	case "usergroups.create":
		f.createUsergroup(w, form)
	case "usergroups.list":
		f.listUsergroups(w, form)
	case "usergroups.update":
		f.withUsergroup(w, form, func(g *slack.UserGroup) {
			if name := form.Get("name"); name != "" {
				g.Name = name
			}
			if handle := form.Get("handle"); handle != "" {
				g.Handle = handle
			}
			if form.Has("description") {
				g.Description = form.Get("description")
			}
			if form.Has("channels") {
				g.Prefs.Channels = splitList(form.Get("channels"))
			}
			writeSlackOK(w, map[string]any{"usergroup": g})
		})
	case "usergroups.users.update":
		f.withUsergroup(w, form, func(g *slack.UserGroup) {
			users := splitList(form.Get("users"))
			if len(users) == 0 {
				writeSlackError(w, "invalid_users")
				return
			}
			g.Users = users
			g.UserCount = len(users)
			writeSlackOK(w, map[string]any{"usergroup": g})
		})
	case "usergroups.enable":
		f.withUsergroup(w, form, func(g *slack.UserGroup) {
			g.DateDelete = 0
			writeSlackOK(w, map[string]any{"usergroup": g})
		})
	case "usergroups.disable":
		f.withUsergroup(w, form, func(g *slack.UserGroup) {
			g.DateDelete = fakeCreatedAt
			writeSlackOK(w, map[string]any{"usergroup": g})
		})
	default:
		writeSlackError(w, "unknown_method")
	}
}

func (f *fakeSlack) createConversation(w http.ResponseWriter, form map[string][]string) {
	name := first(form["name"])
	if name == "" {
		writeSlackError(w, "invalid_name_required")
		return
	}
	if f.nameTaken(name, "") {
		writeSlackError(w, "name_taken")
		return
	}
	f.nextChannelID++
	c := &slack.Channel{}
	c.ID = fmt.Sprintf("C%010d", f.nextChannelID)
	c.Name = name
	c.IsPrivate = first(form["is_private"]) == "true"
	c.Created = fakeCreatedAt
	f.conversations[c.ID] = c
	writeSlackOK(w, map[string]any{"channel": c})
}

func (f *fakeSlack) nameTaken(name, exceptID string) bool {
	for id, c := range f.conversations {
		if id != exceptID && c.Name == name {
			return true
		}
	}
	return false
}

func (f *fakeSlack) withConversation(w http.ResponseWriter, form map[string][]string, fn func(*slack.Channel)) {
	c, ok := f.conversations[first(form["channel"])]
	if !ok {
		writeSlackError(w, "channel_not_found")
		return
	}
	fn(c)
}

func (f *fakeSlack) withActiveConversation(w http.ResponseWriter, form map[string][]string, fn func(*slack.Channel)) {
	f.withConversation(w, form, func(c *slack.Channel) {
		if c.IsArchived {
			writeSlackError(w, "is_archived")
			return
		}
		fn(c)
	})
}

func (f *fakeSlack) createUsergroup(w http.ResponseWriter, form map[string][]string) {
	name := first(form["name"])
	handle := first(form["handle"])
	for _, g := range f.usergroups {
		if g.Name == name {
			writeSlackError(w, "name_already_exists")
			return
		}
		if handle != "" && g.Handle == handle {
			writeSlackError(w, "handle_already_exists")
			return
		}
	}
	f.nextGroupID++
	g := &slack.UserGroup{
		ID:          fmt.Sprintf("S%010d", f.nextGroupID),
		IsUserGroup: true,
		Name:        name,
		Handle:      handle,
		Description: first(form["description"]),
		DateCreate:  fakeCreatedAt,
		Prefs:       slack.UserGroupPrefs{Channels: splitList(first(form["channels"]))},
		Users:       []string{},
	}
	f.usergroups[g.ID] = g
	writeSlackOK(w, map[string]any{"usergroup": g})
}

func (f *fakeSlack) listUsergroups(w http.ResponseWriter, form map[string][]string) {
	includeDisabled := first(form["include_disabled"]) == "true"
	includeUsers := first(form["include_users"]) == "true"
	groups := make([]slack.UserGroup, 0, len(f.usergroups))
	for _, g := range f.usergroups {
		if g.DateDelete != 0 && !includeDisabled {
			continue
		}
		listed := *g
		if !includeUsers {
			listed.Users = nil
		}
		groups = append(groups, listed)
	}
	writeSlackOK(w, map[string]any{"usergroups": groups})
}

func (f *fakeSlack) withUsergroup(w http.ResponseWriter, form map[string][]string, fn func(*slack.UserGroup)) {
	g, ok := f.usergroups[first(form["usergroup"])]
	if !ok {
		writeSlackError(w, "no_such_subteam")
		return
	}
	fn(g)
}

func (f *fakeSlack) withApp(w http.ResponseWriter, form map[string][]string, fn func(string)) {
	id := first(form["app_id"])
	if _, ok := f.apps[id]; !ok {
		writeSlackError(w, "app_not_found")
		return
	}
	fn(id)
}

func parseFakeManifest(w http.ResponseWriter, raw string) (map[string]any, bool) {
	var manifest map[string]any
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		writeSlackError(w, "invalid_manifest")
		return nil, false
	}
	info, _ := manifest["display_information"].(map[string]any)
	if name, _ := info["name"].(string); name == "" {
		writeSlackJSON(w, map[string]any{
			"ok":    false,
			"error": "invalid_manifest",
			"errors": []map[string]any{
				{"message": "must have required property 'name'", "pointer": "/display_information"},
			},
		})
		return nil, false
	}
	return manifest, true
}

// withSlackDefaults mimics apps.manifest.export, which returns keys the
// configured manifest never set.
func withSlackDefaults(manifest map[string]any) map[string]any {
	exported := map[string]any{}
	for k, v := range manifest {
		exported[k] = v
	}
	settings, _ := exported["settings"].(map[string]any)
	merged := map[string]any{"org_deploy_enabled": false, "socket_mode_enabled": false, "token_rotation_enabled": false}
	for k, v := range settings {
		merged[k] = v
	}
	exported["settings"] = merged
	return exported
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func splitList(raw string) []string {
	if raw == "" {
		return []string{}
	}
	return strings.Split(raw, ",")
}

func writeSlackOK(w http.ResponseWriter, fields map[string]any) {
	body := map[string]any{"ok": true}
	for k, v := range fields {
		body[k] = v
	}
	writeSlackJSON(w, body)
}

func writeSlackError(w http.ResponseWriter, code string) {
	writeSlackJSON(w, map[string]any{"ok": false, "error": code})
}

func writeSlackJSON(w http.ResponseWriter, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
