package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
)

// ParamHeader is one tool parameter annotated with `x-mcp-header`. Its value
// travels as the HTTP header `Mcp-Param-{Name}` on Streamable HTTP.
type ParamHeader struct {
	Name string   // the x-mcp-header value, e.g. "Region"
	Path []string // chain of `properties` keys from the schema root
}

// HeaderKey returns the full HTTP header name.
func (p ParamHeader) HeaderKey() string { return HeaderParamPrefix + p.Name }

// schema keywords whose values are data, not subschemas. An "x-mcp-header"
// key inside them is not an annotation.
var schemaDataKeywords = map[string]bool{
	"default": true, "examples": true, "example": true, "const": true, "enum": true,
}

// ParseParamHeaders reads every `x-mcp-header` annotation in a tool's
// inputSchema and validates it against the spec. Any violation makes the
// whole tool invalid; the caller must then hide the tool from clients.
func ParseParamHeaders(inputSchema json.RawMessage) ([]ParamHeader, error) {
	if len(inputSchema) == 0 {
		return nil, nil
	}
	var root any
	if err := json.Unmarshal(inputSchema, &root); err != nil {
		return nil, fmt.Errorf("inputSchema is not valid JSON: %w", err)
	}
	var out []ParamHeader
	seen := map[string]bool{}
	if err := walkSchema(root, nil, true, &out, seen); err != nil {
		return nil, err
	}
	return out, nil
}

func walkSchema(node any, path []string, reachable bool, out *[]ParamHeader, seen map[string]bool) error {
	switch n := node.(type) {
	case []any:
		for _, item := range n {
			if err := walkSchema(item, nil, false, out, seen); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		if raw, ok := n["x-mcp-header"]; ok {
			if err := addParamHeader(n, raw, path, reachable, out, seen); err != nil {
				return err
			}
		}
		for key, val := range n {
			if key == "x-mcp-header" || schemaDataKeywords[key] {
				continue
			}
			if key == "properties" {
				props, ok := val.(map[string]any)
				if !ok {
					continue
				}
				for name, child := range props {
					childPath := append(append([]string(nil), path...), name)
					if err := walkSchema(child, childPath, reachable, out, seen); err != nil {
						return err
					}
				}
				continue
			}
			if err := walkSchema(val, nil, false, out, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

func addParamHeader(schema map[string]any, raw any, path []string, reachable bool, out *[]ParamHeader, seen map[string]bool) error {
	name, ok := raw.(string)
	if !ok || name == "" {
		return errors.New("x-mcp-header must be a non-empty string")
	}
	if !IsToken(name) {
		return fmt.Errorf("x-mcp-header %q is not a valid HTTP header token", name)
	}
	if !reachable || len(path) == 0 {
		return fmt.Errorf("x-mcp-header %q is not on a property reachable through properties only", name)
	}
	switch t, _ := schema["type"].(string); t {
	case "string", "integer", "boolean":
	default:
		return fmt.Errorf("x-mcp-header %q is on a parameter of type %q; only string, integer and boolean are allowed", name, t)
	}
	key := strings.ToLower(name)
	if seen[key] {
		return fmt.Errorf("x-mcp-header %q is used more than once", name)
	}
	seen[key] = true
	*out = append(*out, ParamHeader{Name: name, Path: path})
	return nil
}

// errNoValue means the annotated parameter is absent or null in the arguments,
// so its header is omitted.
var errNoValue = errors.New("no value")

// ParamHeaderValue returns the encoded header value for one annotated
// parameter. ok is false when the argument is absent or null.
func ParamHeaderValue(args json.RawMessage, p ParamHeader) (string, bool, error) {
	if len(args) == 0 {
		return "", false, nil
	}
	path := make([]any, len(p.Path))
	for i, k := range p.Path {
		path[i] = k
	}
	node, err := sonic.Get(args, path...)
	if err != nil {
		if errors.Is(err, ast.ErrNotExist) || isNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read argument %s: %w", strings.Join(p.Path, "."), err)
	}
	v, err := primitiveString(node)
	if errors.Is(err, errNoValue) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("argument %s: %w", strings.Join(p.Path, "."), err)
	}
	return EncodeHeaderValue(v), true, nil
}

func isNotFound(err error) bool {
	var se ast.SyntaxError
	if errors.As(err, &se) {
		return false
	}
	return strings.Contains(err.Error(), "not exist") || strings.Contains(err.Error(), "not found")
}

const maxSafeInteger = 1<<53 - 1

func primitiveString(n ast.Node) (string, error) {
	switch n.TypeSafe() {
	case ast.V_NULL, ast.V_NONE:
		return "", errNoValue
	case ast.V_TRUE:
		return "true", nil
	case ast.V_FALSE:
		return "false", nil
	case ast.V_STRING:
		return n.String()
	case ast.V_NUMBER:
		raw, err := n.Raw()
		if err != nil {
			return "", err
		}
		if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
			if i > maxSafeInteger || i < -maxSafeInteger {
				return "", errors.New("integer outside the safe range")
			}
			return strconv.FormatInt(i, 10), nil
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f != math.Trunc(f) || math.Abs(f) > maxSafeInteger {
			return "", errors.New("not an integer")
		}
		return strconv.FormatInt(int64(f), 10), nil
	default:
		return "", errors.New("not a string, integer or boolean")
	}
}
