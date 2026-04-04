package classify

// targetKeys are argument keys that typically contain the target of an operation,
// ordered by specificity.
var targetKeys = []string{
	"path", "file_path", "filepath", "filename",
	"uri", "url",
	"directory", "dir",
	"name",
	"target",
	"resource",
	"table",
	"database",
	"collection",
	"bucket",
	"key",
	"id",
}

// ExtractTarget extracts the primary target from tool arguments.
// Searches for common target-indicating keys in priority order.
func ExtractTarget(args map[string]interface{}) string {
	for _, key := range targetKeys {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	// fallback: first string value
	for _, v := range args {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}
