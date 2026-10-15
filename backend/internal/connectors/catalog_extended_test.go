package connectors

import (
	"testing"

	"github.com/mydisha/keirouter/backend/internal/core"
)

func TestCatalogIDsAndAliasesUnique(t *testing.T) {
	ids := map[string]bool{}
	aliases := map[string]string{}
	for _, p := range Catalog() {
		if ids[p.ID] {
			t.Errorf("duplicate provider id %q", p.ID)
		}
		ids[p.ID] = true
		if p.Alias == "" {
			continue
		}
		if other, ok := aliases[p.Alias]; ok {
			t.Errorf("alias %q used by %q and %q", p.Alias, other, p.ID)
		}
		aliases[p.Alias] = p.ID
	}
}

func TestCredentialFieldsWellFormed(t *testing.T) {
	for _, p := range Catalog() {
		keys := map[string]bool{}
		for _, f := range p.Fields {
			if f.Key == "" || f.Label == "" {
				t.Errorf("%s: field without key/label: %+v", p.ID, f)
			}
			if keys[f.Key] {
				t.Errorf("%s: duplicate field %q", p.ID, f.Key)
			}
			keys[f.Key] = true
			if f.Default != "" && len(f.Options) > 0 {
				found := false
				for _, o := range f.Options {
					found = found || o.Value == f.Default
				}
				if !found {
					t.Errorf("%s.%s: default %q is not an option", p.ID, f.Key, f.Default)
				}
			}
		}
		conds := []*FieldCondition{p.APIKeyWhen}
		for _, f := range p.Fields {
			conds = append(conds, f.When)
		}
		for _, c := range conds {
			if c != nil && !keys[c.Field] {
				t.Errorf("%s: condition on unknown field %q", p.ID, c.Field)
			}
		}
	}
}

func TestAPIKeyRequirementFollowsAuthMode(t *testing.T) {
	bedrock, ok := SpecByID("bedrock")
	if !ok {
		t.Fatal("bedrock missing")
	}
	if !bedrock.APIKeyRequiredFor(nil) {
		t.Error("bedrock defaults to API key auth, key must be required")
	}
	if bedrock.APIKeyRequiredFor(map[string]string{awsFieldAuth: awsAuthAccessKey}) {
		t.Error("bedrock access-key auth must not require an API key")
	}
	sm, _ := SpecByID("sagemaker")
	if sm.APIKeyVisible(nil) || sm.APIKeyRequiredFor(nil) {
		t.Error("sagemaker has no API key input")
	}
	lm, _ := SpecByID("lm-studio")
	if lm.APIKeyRequiredFor(nil) {
		t.Error("local servers run without a key")
	}
}

func TestEnterpriseDialectsAreRoutable(t *testing.T) {
	reg := DefaultRegistry()
	for _, id := range []string{"bedrock", "sagemaker", "watsonx", "oci", "sap", "replicate", "gigachat",
		"azure-ai", "databricks", "snowflake", "dashscope", "moonshot", "lm-studio", "github-models"} {
		spec, ok := SpecByID(id)
		if !ok {
			t.Errorf("%s missing from catalog", id)
			continue
		}
		if !DrivableDialect(spec.Dialect) {
			t.Errorf("%s: dialect %s is not drivable", id, spec.Dialect)
			continue
		}
		c, err := reg.Get(id)
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		if spec.Dialect != core.DialectOpenAI && c.Dialect() != spec.Dialect {
			t.Errorf("%s: connector dialect %s, want %s", id, c.Dialect(), spec.Dialect)
		}
	}
}
