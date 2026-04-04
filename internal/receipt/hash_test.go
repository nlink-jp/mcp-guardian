package receipt

import (
	"testing"
)

func TestStableStringify(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  string
	}{
		{"empty object", map[string]interface{}{}, "{}"},
		{"sorted keys", map[string]interface{}{"b": 1, "a": 2}, `{"a":2,"b":1}`},
		{"nested", map[string]interface{}{
			"z": map[string]interface{}{"b": "x", "a": "y"},
			"a": 1,
		}, `{"a":1,"z":{"a":"y","b":"x"}}`},
		{"array", []interface{}{3, 1, 2}, `[3,1,2]`},
		{"string", "hello", `"hello"`},
		{"null", nil, "null"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := StableStringify(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestStableStringifyDeterminism(t *testing.T) {
	input := map[string]interface{}{
		"seq": 1, "timestamp": 1234567890,
		"toolName": "write_file", "target": "/tmp/test.txt",
		"arguments": map[string]interface{}{"path": "/tmp/test.txt", "content": "hello"},
		"mutationType": "mutating", "outcome": "success",
		"durationMs": 42, "previousHash": "genesis",
	}

	first, _ := StableStringify(input)
	for i := 0; i < 100; i++ {
		again, _ := StableStringify(input)
		if again != first {
			t.Fatalf("non-deterministic at iteration %d: %s != %s", i, again, first)
		}
	}
}

func TestComputeHash(t *testing.T) {
	r := &Record{
		Seq:          1,
		Timestamp:    1700000000000,
		ToolName:     "read_file",
		Arguments:    map[string]interface{}{"path": "/tmp/test"},
		Target:       "/tmp/test",
		MutationType: "readonly",
		Outcome:      "success",
		DurationMs:   10,
	}

	hash1 := ComputeHash(r, "genesis")
	hash2 := ComputeHash(r, "genesis")
	if hash1 != hash2 {
		t.Fatal("hash is not deterministic")
	}
	if len(hash1) != 64 { // sha256 hex
		t.Fatalf("expected 64 char hex, got %d", len(hash1))
	}

	// Different previous hash should produce different result
	hash3 := ComputeHash(r, "different")
	if hash1 == hash3 {
		t.Fatal("different previous hash should produce different hash")
	}
}
