package provider

import (
	"context"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var testProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"simplevm": providerserver.NewProtocol6WithError(New("test")()),
}

func TestProvider_SchemaValid(t *testing.T) {
	ctx := context.Background()
	resp := &provider.SchemaResponse{}
	New("test")().Schema(ctx, provider.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
	attrs := resp.Schema.Attributes
	for _, name := range []string{"endpoint", "api_key", "project_id"} {
		if _, ok := attrs[name]; !ok {
			t.Fatalf("attribute %q missing", name)
		}
	}
	for name, a := range attrs {
		if !a.IsOptional() {
			t.Errorf("%s should be optional", name)
		}
	}
	if !attrs["api_key"].IsSensitive() {
		t.Error("api_key must be sensitive")
	}
	if _, ok := attrs["endpoint"].(schema.StringAttribute); !ok {
		t.Errorf("endpoint should be a string attribute")
	}
	if _, ok := attrs["project_id"].(schema.Int64Attribute); !ok {
		t.Errorf("project_id should be an int64 attribute")
	}
}

func TestProvider_Metadata(t *testing.T) {
	resp := &provider.MetadataResponse{}
	New("1.2.3")().Metadata(context.Background(), provider.MetadataRequest{}, resp)
	if resp.TypeName != "simplevm" || resp.Version != "1.2.3" {
		t.Fatalf("unexpected metadata: %+v", resp)
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func nullCfg() providerModel {
	return providerModel{Endpoint: types.StringNull(), APIKey: types.StringNull(), ProjectID: types.Int64Null()}
}

func TestResolveConfig_MissingAPIKey(t *testing.T) {
	_, diags := resolveConfig(nullCfg(), env(nil))
	if !diags.HasError() || diags[0].Summary() != "Missing SimpleVM API key" ||
		diags[0].Detail() != "api_key must be set or SIMPLEVM_API_KEY exported" {
		t.Fatalf("expected missing api_key error, got %v", diags)
	}
}

func TestResolveConfig_EnvFallbackAndPrecedence(t *testing.T) {
	e := env(map[string]string{"SIMPLEVM_API_KEY": "envkey", "SIMPLEVM_ENDPOINT": "https://env/", "SIMPLEVM_PROJECT_ID": "7"})
	got, diags := resolveConfig(nullCfg(), e)
	if diags.HasError() || got.APIKey != "envkey" || got.Endpoint != "https://env/" || got.ProjectID != 7 || !got.HasProjectID {
		t.Fatalf("env fallback failed: %+v %v", got, diags)
	}

	cfg := providerModel{Endpoint: types.StringValue("https://cfg/"), APIKey: types.StringValue("cfgkey"), ProjectID: types.Int64Value(9)}
	got, diags = resolveConfig(cfg, e)
	if diags.HasError() || got.APIKey != "cfgkey" || got.Endpoint != "https://cfg/" || got.ProjectID != 9 {
		t.Fatalf("config precedence failed: %+v %v", got, diags)
	}
}

func TestResolveConfig_Defaults(t *testing.T) {
	got, diags := resolveConfig(nullCfg(), env(map[string]string{"SIMPLEVM_API_KEY": "k"}))
	if diags.HasError() || got.Endpoint != DefaultEndpoint || got.HasProjectID {
		t.Fatalf("unexpected defaults: %+v %v", got, diags)
	}
}

func TestResolveConfig_BadProjectIDEnv(t *testing.T) {
	_, diags := resolveConfig(nullCfg(), env(map[string]string{"SIMPLEVM_API_KEY": "k", "SIMPLEVM_PROJECT_ID": "abc"}))
	if !diags.HasError() {
		t.Fatal("expected error for non-integer project id")
	}
}

func TestResolveConfig_Unknown(t *testing.T) {
	cfg := nullCfg()
	cfg.APIKey = types.StringUnknown()
	_, diags := resolveConfig(cfg, env(map[string]string{"SIMPLEVM_API_KEY": "k"}))
	if !diags.HasError() {
		t.Fatal("expected error for unknown api_key")
	}
}

func TestProvider_ServesOverProtocol6(t *testing.T) {
	srv, err := testProtoV6ProviderFactories["simplevm"]()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Diagnostics) > 0 || resp.Provider == nil {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestParsePollInterval(t *testing.T) {
	if d, err := parsePollInterval(""); err != nil || d != DefaultPollInterval {
		t.Fatalf("default: %v %v", d, err)
	}
	if d, err := parsePollInterval("250ms"); err != nil || d != 250*time.Millisecond {
		t.Fatalf("valid: %v %v", d, err)
	}
	for _, bad := range []string{"abc", "0s", "-5s", "10"} {
		if _, err := parsePollInterval(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}
