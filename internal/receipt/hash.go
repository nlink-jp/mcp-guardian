package receipt

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// StableStringify produces a deterministic JSON string with sorted keys.
// This is critical for hash chain integrity across implementations.
func StableStringify(v interface{}) (string, error) {
	sorted, err := sortKeys(v)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(sorted)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// sortKeys recursively sorts map keys for deterministic JSON output.
func sortKeys(v interface{}) (interface{}, error) {
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ordered := make(orderedMap, 0, len(keys))
		for _, k := range keys {
			sorted, err := sortKeys(val[k])
			if err != nil {
				return nil, err
			}
			ordered = append(ordered, kv{Key: k, Value: sorted})
		}
		return ordered, nil
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			sorted, err := sortKeys(item)
			if err != nil {
				return nil, err
			}
			result[i] = sorted
		}
		return result, nil
	default:
		return v, nil
	}
}

// orderedMap preserves insertion order during JSON marshaling.
type orderedMap []kv

type kv struct {
	Key   string
	Value interface{}
}

func (om orderedMap) MarshalJSON() ([]byte, error) {
	buf := []byte{'{'}
	for i, pair := range om {
		if i > 0 {
			buf = append(buf, ',')
		}
		key, _ := json.Marshal(pair.Key)
		val, err := json.Marshal(pair.Value)
		if err != nil {
			return nil, err
		}
		buf = append(buf, key...)
		buf = append(buf, ':')
		buf = append(buf, val...)
	}
	buf = append(buf, '}')
	return buf, nil
}

// ComputeHash computes the SHA-256 hash for a record given the previous hash.
// The hash covers: seq, timestamp, toolName, arguments, target, mutationType,
// outcome, durationMs, and previousHash.
func ComputeHash(r *Record, previousHash string) string {
	payload := map[string]interface{}{
		"seq":          r.Seq,
		"timestamp":    r.Timestamp,
		"toolName":     r.ToolName,
		"arguments":    r.Arguments,
		"target":       r.Target,
		"mutationType": r.MutationType,
		"outcome":      r.Outcome,
		"durationMs":   r.DurationMs,
		"previousHash": previousHash,
	}
	str, _ := StableStringify(payload)
	sum := sha256.Sum256([]byte(str))
	return fmt.Sprintf("%x", sum)
}
