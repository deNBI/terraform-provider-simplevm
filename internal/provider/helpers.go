package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// resolveProjectID returns the project from the resource/data source attribute,
// falling back to the provider default.
func resolveProjectID(attr types.Int64, pd *ProviderData) (int64, diag.Diagnostics) {
	var diags diag.Diagnostics
	if !attr.IsNull() && !attr.IsUnknown() {
		return attr.ValueInt64(), diags
	}
	if pd != nil && pd.DefaultProjectID != 0 {
		return pd.DefaultProjectID, diags
	}
	diags.AddError("Missing project_id", "project_id must be set on the resource or the provider")
	return 0, diags
}

// providerData converts the ProviderData handed to Configure; nil raw (the
// provider is not configured yet) yields nil without error.
func providerData(raw any) (*ProviderData, diag.Diagnostics) {
	var diags diag.Diagnostics
	if raw == nil {
		return nil, diags
	}
	pd, ok := raw.(*ProviderData)
	if !ok {
		diags.AddError("Unexpected provider data type",
			fmt.Sprintf("Expected *provider.ProviderData, got %T. This is a bug in the provider.", raw))
		return nil, diags
	}
	return pd, diags
}

// sortedJoin returns the sorted names joined with ", ".
func sortedJoin(names []string) string {
	s := slices.Clone(names)
	slices.Sort(s)
	return strings.Join(s, ", ")
}

// providerDataInto is providerData for Configure methods: diagnostics are
// appended to diags. A nil result means the provider is not configured yet.
func providerDataInto(raw any, diags *diag.Diagnostics) (*ProviderData, bool) {
	pd, d := providerData(raw)
	diags.Append(d...)
	return pd, !d.HasError()
}

func pathRoot(name string) path.Path { return path.Root(name) }

// opTimeouts are the default timeouts of a resource; zero means the operation
// has no configurable timeout.
type opTimeouts struct {
	Create, Update, Delete time.Duration
}

// timeoutsAttribute returns the `timeouts` attribute for the operations with a
// non-zero default, with MarkdownDescriptions naming each default.
func timeoutsAttribute(ctx context.Context, d opTimeouts) schema.Attribute {
	a := timeouts.Attributes(ctx, timeouts.Opts{Create: d.Create != 0, Update: d.Update != 0, Delete: d.Delete != 0})
	nested, ok := a.(schema.SingleNestedAttribute)
	if !ok {
		return a
	}
	defaults := map[string]time.Duration{"create": d.Create, "update": d.Update, "delete": d.Delete}
	for name, na := range nested.Attributes {
		sa, ok := na.(schema.StringAttribute)
		if !ok {
			continue
		}
		sa.MarkdownDescription = fmt.Sprintf("Timeout for the %s operation (default `%s`). %s",
			name, strings.TrimSuffix(defaults[name].String(), "0s"), sa.Description)
		nested.Attributes[name] = sa
	}
	nested.MarkdownDescription = "Timeouts for long-running operations, as Go durations such as `30m` or `1h`."
	return nested
}
