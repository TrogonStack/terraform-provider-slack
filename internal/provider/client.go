package provider

import (
	"github.com/slack-go/slack"
)

type apiClient struct {
	slack *slack.Client
}
