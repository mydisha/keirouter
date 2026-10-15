package connectors

import (
	"strings"

	"github.com/mydisha/keirouter/backend/internal/core"
)

// CredentialField is one provider-specific input on the connect form (cloud
// region, project id, a second secret, ...). Values reach the connector through
// Credentials.Extra under Key. Secret values are sealed alongside the API key;
// everything else is stored as plain account metadata.
type CredentialField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Hint        string `json:"hint,omitempty"`
	// Required fields must be non-empty whenever the field is visible.
	Required bool `json:"required,omitempty"`
	// Secret fields are encrypted at rest and rendered as password inputs.
	Secret bool `json:"secret,omitempty"`
	// Multiline renders a textarea (PEM keys, JSON service keys).
	Multiline bool `json:"multiline,omitempty"`
	// URL values pass the same SSRF policy as base URLs.
	URL bool `json:"url,omitempty"`
	// Advanced fields are tucked under the form's "Advanced" section.
	Advanced bool          `json:"advanced,omitempty"`
	Default  string        `json:"default,omitempty"`
	Options  []FieldOption `json:"options,omitempty"`
	// When limits the field to a value of another (select) field.
	When *FieldCondition `json:"when,omitempty"`
}

// FieldOption is one choice of a select-style field.
type FieldOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// FieldCondition makes a field (or the API key input) visible only while the
// field named Field holds one of Values.
type FieldCondition struct {
	Field  string   `json:"field"`
	Values []string `json:"values"`
}

// API key input modes for ProviderSpec.APIKeyMode.
const (
	// APIKeyRequired is the default: the key input is shown and required.
	APIKeyRequired = ""
	// APIKeyOptional shows the key but lets other fields authenticate instead.
	APIKeyOptional = "optional"
	// APIKeyHidden removes the key input; Fields carry every credential.
	APIKeyHidden = "hidden"
)

// Matches reports whether the condition holds for the given field values,
// falling back to each field's default when the value is unset.
func (c *FieldCondition) Matches(spec ProviderSpec, values map[string]string) bool {
	if c == nil {
		return true
	}
	v := strings.TrimSpace(values[c.Field])
	if v == "" {
		if f, ok := spec.Field(c.Field); ok {
			v = f.Default
		}
	}
	for _, want := range c.Values {
		if v == want {
			return true
		}
	}
	return false
}

// Field returns the credential field with the given key.
func (p ProviderSpec) Field(key string) (CredentialField, bool) {
	for _, f := range p.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return CredentialField{}, false
}

// APIKeyVisible reports whether the API key input applies for these values.
func (p ProviderSpec) APIKeyVisible(values map[string]string) bool {
	if p.APIKeyMode == APIKeyHidden {
		return false
	}
	return p.APIKeyWhen.Matches(p, values)
}

// APIKeyRequiredFor reports whether an API key must be supplied for these
// field values.
func (p ProviderSpec) APIKeyRequiredFor(values map[string]string) bool {
	if p.AuthKind == "none" || p.APIKeyMode == APIKeyOptional {
		return false
	}
	return p.APIKeyVisible(values)
}

// extraValue returns a trimmed credential field value.
func extraValue(creds core.Credentials, key string) string {
	return strings.TrimSpace(creds.Extra[key])
}

// extraOr returns a credential field value, or def when unset.
func extraOr(creds core.Credentials, key, def string) string {
	if v := extraValue(creds, key); v != "" {
		return v
	}
	return def
}

func opts(pairs ...string) []FieldOption {
	out := make([]FieldOption, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, FieldOption{Value: pairs[i], Label: pairs[i+1]})
	}
	return out
}

func when(field string, values ...string) *FieldCondition {
	return &FieldCondition{Field: field, Values: values}
}
