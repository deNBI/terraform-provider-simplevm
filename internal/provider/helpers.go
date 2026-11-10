package provider

import (
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
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
