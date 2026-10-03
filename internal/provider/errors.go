package provider

import (
	"errors"
	"strings"

	"github.com/slack-go/slack"
)

const (
	errChannelNotFound     = "channel_not_found"
	errAlreadyArchived     = "already_archived"
	errNotArchived         = "not_archived"
	errNameAlreadyExists   = "name_already_exists"
	errHandleAlreadyExists = "handle_already_exists"
	errAppNotFound         = "app_not_found"
	errInvalidAppID        = "invalid_app_id"
)

func slackErrorCode(err error) string {
	var slackErr slack.SlackErrorResponse
	if !errors.As(err, &slackErr) {
		return ""
	}
	return slackErr.Err
}

func hasSlackError(err error, codes ...string) bool {
	code := slackErrorCode(err)
	if code == "" {
		return false
	}
	for _, c := range codes {
		if code == c {
			return true
		}
	}
	return false
}

func describeSlackError(err error) string {
	var slackErr slack.SlackErrorResponse
	if !errors.As(err, &slackErr) || len(slackErr.ResponseMetadata.Messages) == 0 {
		return err.Error()
	}
	return slackErr.Err + "\n" + strings.Join(slackErr.ResponseMetadata.Messages, "\n")
}

func isNotFound(err error) bool {
	return hasSlackError(err, errChannelNotFound)
}
