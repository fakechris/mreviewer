package db

import (
	"database/sql/driver"
	"fmt"
)

// NullRawMessage preserves nullable JSON columns without forcing generated
// scan code to special-case NULL database values.
type NullRawMessage []byte

func (m *NullRawMessage) Scan(src any) error {
	if src == nil {
		*m = nil
		return nil
	}

	switch v := src.(type) {
	case []byte:
		*m = append((*m)[:0], v...)
	case string:
		*m = append((*m)[:0], v...)
	default:
		*m = (*m)[:0]
		return fmt.Errorf("db.NullRawMessage.Scan: unsupported src type %T", src)
	}
	return nil
}

func (m NullRawMessage) Value() (driver.Value, error) {
	if m == nil {
		return nil, nil
	}
	return []byte(m), nil
}
