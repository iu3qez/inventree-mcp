package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
)

// ipnPlaceholder matches {pk} and {pk:Nd} / {pk:0Nd} in an IPN template.
var ipnPlaceholder = regexp.MustCompile(`\{pk(?::(0?)(\d+)d)?\}`)

// checkIPNInput rejects a request that gives both a literal IPN and a
// template, or a template that cannot produce one. It runs before the part is
// created, so a bad template never leaves a part behind.
func checkIPNInput(ipn, template string) error {
	if template == "" {
		return nil
	}
	if ipn != "" {
		return fmt.Errorf("IPN and ipn_template are mutually exclusive")
	}
	_, err := formatIPN(template, 0)
	return err
}

// formatIPN expands every {pk} placeholder in template with pk.
func formatIPN(template string, pk int) (string, error) {
	if !ipnPlaceholder.MatchString(template) {
		return "", fmt.Errorf("ipn_template %q has no {pk} placeholder", template)
	}
	out := ipnPlaceholder.ReplaceAllStringFunc(template, func(m string) string {
		sub := ipnPlaceholder.FindStringSubmatch(m)
		if sub[2] == "" {
			return strconv.Itoa(pk)
		}
		width, _ := strconv.Atoi(sub[2])
		if sub[1] == "0" {
			return fmt.Sprintf("%0*d", width, pk)
		}
		return fmt.Sprintf("%*d", width, pk)
	})
	if strings.ContainsAny(out, "{}") {
		return "", fmt.Errorf("ipn_template %q has an unsupported placeholder: use {pk} or {pk:05d}", template)
	}
	return out, nil
}

// applyIPNTemplate sets the IPN of a just-created part from its pk.
func applyIPNTemplate(c *client.Client, pk int, template string) (Part, error) {
	var updated Part
	ipn, err := formatIPN(template, pk)
	if err != nil {
		return updated, err
	}
	if err := c.Patch(fmt.Sprintf("/api/part/%d/", pk), map[string]any{"IPN": ipn}, &updated); err != nil {
		return updated, fmt.Errorf("setting IPN %q: %w", ipn, err)
	}
	return updated, nil
}
