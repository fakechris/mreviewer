package sqlitedb

import (
	"encoding/json"
	"fmt"
)

// jsonScanner wraps a *json.RawMessage so it can scan from SQLite TEXT columns
// (which return string, not []byte like MySQL does).
type jsonScanner struct {
	dest *json.RawMessage
}

func jscan(dest *json.RawMessage) *jsonScanner {
	return &jsonScanner{dest: dest}
}

func (j *jsonScanner) Scan(src interface{}) error {
	if src == nil {
		*j.dest = nil
		return nil
	}
	switch v := src.(type) {
	case []byte:
		cp := make(json.RawMessage, len(v))
		copy(cp, v)
		*j.dest = cp
		return nil
	case string:
		*j.dest = json.RawMessage(v)
		return nil
	default:
		return fmt.Errorf("jsonScanner: unsupported type %T", src)
	}
}
