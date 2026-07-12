package adminv1

// plan_helpers.go contains pure-function utilities used by the typed plan
// write handlers. These are duplicated from internal/admin/handle_plans.go
// so that the typed handler has no import dependency on the legacy chi-based
// admin package. The two copies will be unified once Batch 14 removes the
// last legacy chi handler that references the originals.

import (
	"encoding/json"
	"fmt"

	"github.com/modelserver/modelserver/internal/modelcatalog"
	"github.com/modelserver/modelserver/internal/types"
)

// normalizeRateMapKeys normalizes every non-sentinel key of a model-credit-rate
// map against the catalog. The sentinel `_default` is preserved verbatim —
// it is a plan-wide fallback, not a model name.
func normalizeRateMapKeys(catalog modelcatalog.Catalog, in map[string]types.CreditRate) (map[string]types.CreditRate, error) {
	if len(in) == 0 {
		return in, nil
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		if k == "_default" {
			continue
		}
		keys = append(keys, k)
	}
	canonical, err := catalog.NormalizeNames(keys)
	if err != nil {
		return nil, err
	}
	out := make(map[string]types.CreditRate, len(in))
	for i, k := range keys {
		out[canonical[i]] = in[k]
	}
	if def, ok := in["_default"]; ok {
		out["_default"] = def
	}
	return out, nil
}

// normalizeRateMapKeysRaw is the map[string]json.RawMessage variant used by the
// typed updatePlan handler. It normalizes model-name keys against the catalog,
// preserving the _default sentinel verbatim. Complex values stay as raw JSON.
func normalizeRateMapKeysRaw(catalog modelcatalog.Catalog, raw map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	keys := make([]string, 0, len(raw))
	for k := range raw {
		if k == "_default" {
			continue
		}
		keys = append(keys, k)
	}
	canonical, err := catalog.NormalizeNames(keys)
	if err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(raw))
	for i, k := range keys {
		out[canonical[i]] = raw[k]
	}
	if def, ok := raw["_default"]; ok {
		out["_default"] = def
	}
	return out, nil
}

// validateCreditRules checks for invalid CreditRule configurations.
func validateCreditRules(rules []types.CreditRule) error {
	for _, rule := range rules {
		if rule.WindowType == types.WindowTypeFixed && len(rule.Window) > 0 && rule.Window[len(rule.Window)-1] == 'M' {
			return fmt.Errorf("month-based window %q is not supported with window_type \"fixed\" — use duration-based intervals like \"7d\"", rule.Window)
		}
	}
	return nil
}
