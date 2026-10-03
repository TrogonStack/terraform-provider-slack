package provider

import (
	"errors"
	"fmt"
	"testing"

	"github.com/slack-go/slack"
)

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "channel not found",
			err:  slack.SlackErrorResponse{Err: "channel_not_found"},
			want: true,
		},
		{
			name: "wrapped channel not found",
			err:  fmt.Errorf("wrapped: %w", slack.SlackErrorResponse{Err: "channel_not_found"}),
			want: true,
		},
		{
			name: "other slack error",
			err:  slack.SlackErrorResponse{Err: "not_in_channel"},
			want: false,
		},
		{
			name: "status code error",
			err:  slack.StatusCodeError{Code: 404, Status: "404 Not Found"},
			want: false,
		},
		{
			name: "non slack error",
			err:  errors.New("boom"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNotFound(tt.err); got != tt.want {
				t.Errorf("isNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestHasSlackError(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", slack.SlackErrorResponse{Err: "name_already_exists"})
	if !hasSlackError(err, errNameAlreadyExists, errHandleAlreadyExists) {
		t.Error("expected name_already_exists to match")
	}
	if hasSlackError(err, errHandleAlreadyExists) {
		t.Error("expected name_already_exists not to match handle_already_exists")
	}
	if hasSlackError(nil, errNameAlreadyExists) {
		t.Error("expected nil error not to match")
	}
}
