package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ParameterTemplate represents an InvenTree parameter template.
type ParameterTemplate struct {
	PK          int    `json:"pk"`
	Name        string `json:"name"`
	Units       string `json:"units"`
	Description string `json:"description"`
	Checkbox    bool   `json:"checkbox"`
	Choices     string `json:"choices"`
	ModelType   string `json:"model_type"`
}

// Parameter represents a parameter value attached to a part.
//
// The generic API (InvenTree API >= 430) identifies the owner with
// model_type/model_id; the legacy API used a "part" field instead. Both are
// decoded here so the same struct serves either backend.
type Parameter struct {
	PK             int                `json:"pk"`
	Template       int                `json:"template"`
	TemplateDetail *ParameterTemplate `json:"template_detail"`
	Data           string             `json:"data"`
	DataNumeric    *float64           `json:"data_numeric"`
	Note           string             `json:"note"`

	ModelType string `json:"model_type,omitempty"`
	ModelID   int    `json:"model_id,omitempty"`
	Part      int    `json:"part,omitempty"`
}

// paramAPI describes which flavour of the parameter API the server exposes.
//
// InvenTree API v430 (2025-12-04) removed /api/part/parameter/ and replaced it
// with the generic /api/parameter/ endpoints, which address the owning object
// via model_type + model_id. Older servers only have the legacy paths.
type paramAPI struct {
	parameters string
	templates  string
	generic    bool
}

var genericParamAPI = &paramAPI{
	parameters: "/api/parameter/",
	templates:  "/api/parameter/template/",
	generic:    true,
}

var legacyParamAPI = &paramAPI{
	parameters: "/api/part/parameter/",
	templates:  "/api/part/parameter/template/",
	generic:    false,
}

// paramAPIResolver detects the parameter API flavour once and caches it.
// Detection failures that are not a 404 (network, auth) are not cached, so a
// transient error does not pin the server to the wrong endpoint for its
// lifetime.
type paramAPIResolver struct {
	mu       sync.Mutex
	resolved *paramAPI
}

func newParamAPIResolver() *paramAPIResolver { return &paramAPIResolver{} }

func (r *paramAPIResolver) resolve(c *client.Client) (*paramAPI, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.resolved != nil {
		return r.resolved, nil
	}

	var probe client.PaginatedResponse[ParameterTemplate]
	err := c.Get(genericParamAPI.templates+"?limit=1", &probe)
	switch {
	case err == nil:
		r.resolved = genericParamAPI
	case client.StatusCode(err) == 404:
		r.resolved = legacyParamAPI
	default:
		return nil, fmt.Errorf("probing parameter API: %w", err)
	}
	return r.resolved, nil
}

// findTemplate looks up a parameter template by name, returning nil without
// error when nothing matches.
//
// The exact "name" filter is tried first, then a fuzzy "search" pass so a
// differently-cased name still matches an existing template instead of
// creating a near-duplicate. Results are always confirmed locally, which also
// keeps this correct on servers that ignore an unknown filter.
func (p *paramAPI) findTemplate(c *client.Client, name string) (*ParameterTemplate, error) {
	name = strings.TrimSpace(name)

	for _, filter := range []string{"name", "search"} {
		path := fmt.Sprintf("%s?%s=%s&limit=50&format=json", p.templates, filter, url.QueryEscape(name))
		var resp client.PaginatedResponse[ParameterTemplate]
		if err := c.Get(path, &resp); err != nil {
			return nil, fmt.Errorf("searching parameter templates: %w", err)
		}
		for i := range resp.Results {
			if strings.EqualFold(strings.TrimSpace(resp.Results[i].Name), name) {
				return &resp.Results[i], nil
			}
		}
	}
	return nil, nil
}

// createTemplate creates a new parameter template for parts.
func (p *paramAPI) createTemplate(c *client.Client, name, units, description string) (*ParameterTemplate, error) {
	payload := map[string]any{"name": name}
	if units != "" {
		payload["units"] = units
	}
	if description != "" {
		payload["description"] = description
	}
	if p.generic {
		payload["model_type"] = "part"
	}

	var created ParameterTemplate
	if err := c.Post(p.templates, payload, &created); err != nil {
		return nil, fmt.Errorf("creating parameter template %q: %w", name, err)
	}
	return &created, nil
}

// findOrCreateTemplate returns an existing template by name, creating it when
// missing (and allowed).
func (p *paramAPI) findOrCreateTemplate(c *client.Client, name, units, description string, allowCreate bool) (*ParameterTemplate, error) {
	tmpl, err := p.findTemplate(c, name)
	if err != nil {
		return nil, err
	}
	if tmpl != nil {
		return tmpl, nil
	}
	if !allowCreate {
		return nil, fmt.Errorf("no parameter template named %q (set create_templates to true to add it)", name)
	}
	return p.createTemplate(c, name, units, description)
}

// listForPart returns all parameters attached to a part.
func (p *paramAPI) listForPart(c *client.Client, partID int) ([]Parameter, error) {
	var path string
	if p.generic {
		path = fmt.Sprintf("%s?model_type=part&model_id=%d&limit=250&format=json", p.parameters, partID)
	} else {
		path = fmt.Sprintf("%s?part=%d&limit=250&format=json", p.parameters, partID)
	}

	var resp client.PaginatedResponse[Parameter]
	if err := c.Get(path, &resp); err != nil {
		return nil, fmt.Errorf("listing parameters for part %d: %w", partID, err)
	}
	return resp.Results, nil
}

// setValue creates or updates the parameter for (part, template).
// The returned string reports which action was taken.
func (p *paramAPI) setValue(c *client.Client, partID, templateID int, value, note string, existing []Parameter) (string, *Parameter, error) {
	for i := range existing {
		if existing[i].Template != templateID {
			continue
		}
		// Already at the requested value: skip the write entirely.
		if existing[i].Data == value && (note == "" || existing[i].Note == note) {
			return "unchanged", &existing[i], nil
		}
		payload := map[string]any{"data": value}
		if note != "" {
			payload["note"] = note
		}
		var updated Parameter
		path := fmt.Sprintf("%s%d/", p.parameters, existing[i].PK)
		if err := c.Patch(path, payload, &updated); err != nil {
			return "", nil, err
		}
		return "updated", &updated, nil
	}

	payload := map[string]any{
		"template": templateID,
		"data":     value,
	}
	if p.generic {
		payload["model_type"] = "part"
		payload["model_id"] = partID
	} else {
		payload["part"] = partID
	}
	if note != "" {
		payload["note"] = note
	}

	var created Parameter
	if err := c.Post(p.parameters, payload, &created); err != nil {
		return "", nil, err
	}
	return "created", &created, nil
}

// -- Get Part Parameters --

type GetPartParametersInput struct {
	Part int `json:"part" jsonschema:"The part ID (pk) whose parameters to list"`
}

func RegisterGetPartParameters(server *mcp.Server, c *client.Client, res *paramAPIResolver, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "get_part_parameters",
		Description: "List all parameters (technical specifications) attached to a part, with their template names, values and units. Use this to inspect what specs a part already carries before adding more.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetPartParametersInput) (*mcp.CallToolResult, any, error) {
		api, err := res.resolve(c)
		if err != nil {
			return errResult(err), nil, nil
		}
		params, err := api.listForPart(c, input.Part)
		if err != nil {
			return errResult(err), nil, nil
		}

		out := make([]map[string]any, 0, len(params))
		for _, p := range params {
			entry := map[string]any{
				"pk":       p.PK,
				"template": p.Template,
				"value":    p.Data,
			}
			if p.TemplateDetail != nil {
				entry["name"] = p.TemplateDetail.Name
				if p.TemplateDetail.Units != "" {
					entry["units"] = p.TemplateDetail.Units
				}
			}
			if p.Note != "" {
				entry["note"] = p.Note
			}
			out = append(out, entry)
		}
		return jsonResult(map[string]any{
			"part":       input.Part,
			"count":      len(out),
			"parameters": out,
		})
	})
}

// -- Set Part Parameters --

type ParameterValueInput struct {
	Name  string `json:"name" jsonschema:"Parameter name, matched against existing templates case-insensitively (e.g. 'Resistance', 'Tolerance', 'Package Type')"`
	Value string `json:"value" jsonschema:"Parameter value as text (e.g. '10k', '1%', 'SOT-23')"`
	Units string `json:"units,omitempty" jsonschema:"Physical units, used only when a new template has to be created (e.g. 'ohm', 'V', 'F')"`
	Note  string `json:"note,omitempty" jsonschema:"Optional note stored alongside the value"`
}

type SetPartParametersInput struct {
	Part            int                   `json:"part" jsonschema:"The part ID (pk) to set parameters on"`
	Parameters      []ParameterValueInput `json:"parameters" jsonschema:"Parameters to set. Existing values for the same template are overwritten."`
	CreateTemplates *bool                 `json:"create_templates,omitempty" jsonschema:"Create parameter templates that do not exist yet (default true)"`
}

func RegisterSetPartParameters(server *mcp.Server, c *client.Client, res *paramAPIResolver, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "set_part_parameters",
		Description: "Set technical parameters on a part in one call, e.g. resistance, tolerance, package, voltage rating. " +
			"Parameter templates are matched by name (case-insensitive) and created on demand, so you can pass a datasheet's specs directly. " +
			"Existing values for the same parameter are overwritten. Prefer names already used in the instance — call list_parameter_templates first if unsure.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SetPartParametersInput) (*mcp.CallToolResult, any, error) {
		if input.Part == 0 {
			return errResult(fmt.Errorf("part is required")), nil, nil
		}
		if len(input.Parameters) == 0 {
			return errResult(fmt.Errorf("at least one parameter is required")), nil, nil
		}

		api, err := res.resolve(c)
		if err != nil {
			return errResult(err), nil, nil
		}

		allowCreate := input.CreateTemplates == nil || *input.CreateTemplates

		existing, err := api.listForPart(c, input.Part)
		if err != nil {
			return errResult(err), nil, nil
		}

		results := make([]map[string]any, 0, len(input.Parameters))
		failures := 0
		for _, p := range input.Parameters {
			name := strings.TrimSpace(p.Name)
			if name == "" {
				results = append(results, map[string]any{"name": p.Name, "status": "error", "error": "empty parameter name"})
				failures++
				continue
			}

			tmpl, err := api.findOrCreateTemplate(c, name, p.Units, "", allowCreate)
			if err != nil {
				results = append(results, map[string]any{"name": name, "status": "error", "error": err.Error()})
				failures++
				continue
			}

			action, saved, err := api.setValue(c, input.Part, tmpl.PK, p.Value, p.Note, existing)
			if err != nil {
				results = append(results, map[string]any{"name": name, "status": "error", "error": err.Error()})
				failures++
				continue
			}
			if action == "created" && saved != nil {
				// Keep the local cache in sync so a duplicate name in the same
				// call updates instead of failing on the uniqueness constraint.
				existing = append(existing, *saved)
			}
			results = append(results, map[string]any{
				"name":     name,
				"template": tmpl.PK,
				"value":    p.Value,
				"status":   action,
			})
		}

		return jsonResult(map[string]any{
			"part":       input.Part,
			"count":      len(results),
			"failed":     failures,
			"parameters": results,
		})
	})
}

// -- List Parameter Templates --

type ListParameterTemplatesInput struct {
	Search string `json:"search,omitempty" jsonschema:"Filter templates by name. Omit to list all."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 100)"`
}

func RegisterListParameterTemplates(server *mcp.Server, c *client.Client, res *paramAPIResolver, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "list_parameter_templates",
		Description: "List existing parameter templates (the named specs available across the instance, e.g. 'Resistance', 'Package Type'). Use this before set_part_parameters to reuse the naming already in place instead of creating near-duplicate templates.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListParameterTemplatesInput) (*mcp.CallToolResult, any, error) {
		api, err := res.resolve(c)
		if err != nil {
			return errResult(err), nil, nil
		}

		limit := input.Limit
		if limit <= 0 {
			limit = 100
		}
		path := fmt.Sprintf("%s?limit=%d&format=json", api.templates, limit)
		if input.Search != "" {
			path += "&search=" + url.QueryEscape(input.Search)
		}

		var resp client.PaginatedResponse[ParameterTemplate]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("listing parameter templates: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}
