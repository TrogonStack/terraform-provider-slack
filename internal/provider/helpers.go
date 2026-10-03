package provider

import (
	"context"
	"sort"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/slack-go/slack"
)

func rsId() schema.StringAttribute {
	return schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: "The unique ID of this resource.",
		PlanModifiers: []planmodifier.String{
			stringplanmodifier.UseStateForUnknown(),
		},
	}
}

func timestampValue(t slack.JSONTime) types.String {
	if t == 0 {
		return types.StringNull()
	}
	return types.StringValue(t.Time().UTC().Format(time.RFC3339))
}

func isConfigured(v types.String) bool {
	return !v.IsNull() && !v.IsUnknown()
}

func setStrings(ctx context.Context, v types.Set) ([]string, diag.Diagnostics) {
	if v.IsNull() || v.IsUnknown() {
		return nil, nil
	}
	out := []string{}
	diags := v.ElementsAs(ctx, &out, false)
	sort.Strings(out)
	return out, diags
}
