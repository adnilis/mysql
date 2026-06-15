package plugins

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strconv"
)

// NullInt64 R10:可空列自动扫描,NULL → 0(无需用户写 sql.NullInt64)
//
// 用法:
//
//	type User struct {
//	    ID    NullInt64 `db:"id"`    // 实际值 0/42
//	    Score NullInt64 `db:"score"` // 实际值 0/100
//	}
//
// 内部走 sql.Scanner 接口;NULL 时 Valid=false,非 NULL 时 Valid=true。
// 完全替代 sql.NullInt64,代码更简洁。
type NullInt64 struct {
	Valid bool  // 显式赋值标记
	Int64 int64 // 实际值(NULL 时为 0)
}

// Scan 实现 sql.Scanner(R10)
func (n *NullInt64) Scan(src any) error {
	if src == nil {
		n.Valid = false
		n.Int64 = 0
		return nil
	}
	var v int64
	switch x := src.(type) {
	case int64:
		v = x
	case int:
		v = int64(x)
	case int32:
		v = int64(x)
	case []byte:
		parsed, err := strconv.ParseInt(string(x), 10, 64)
		if err != nil {
			return fmt.Errorf("NullInt64: parse %q: %w", x, err)
		}
		v = parsed
	case string:
		parsed, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return fmt.Errorf("NullInt64: parse %q: %w", x, err)
		}
		v = parsed
	default:
		return fmt.Errorf("NullInt64: unsupported source type %T", src)
	}
	n.Int64 = v
	n.Valid = true
	return nil
}

// Value 实现 driver.Valuer(R10)
func (n NullInt64) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Int64, nil
}

// NullString 可空字符串(R10)
type NullString struct {
	Valid bool
	Str   string
}

// Scan 实现 sql.Scanner
func (n *NullString) Scan(src any) error {
	if src == nil {
		n.Valid = false
		n.Str = ""
		return nil
	}
	var v string
	switch x := src.(type) {
	case string:
		v = x
	case []byte:
		v = string(x)
	default:
		return fmt.Errorf("NullString: unsupported source type %T", src)
	}
	n.Str = v
	n.Valid = true
	return nil
}

// Value 实现 driver.Valuer
func (n NullString) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Str, nil
}

// 工厂方法
func NewNullInt64(v int64) NullInt64 {
	return NullInt64{Valid: true, Int64: v}
}

func NewNullInt64FromPtr(p *int64) NullInt64 {
	if p == nil {
		return NullInt64{}
	}
	return NullInt64{Valid: true, Int64: *p}
}

func NewNullString(v string) NullString {
	return NullString{Valid: true, Str: v}
}

func NewNullStringFromPtr(p *string) NullString {
	if p == nil {
		return NullString{}
	}
	return NullString{Valid: true, Str: *p}
}

// 编译期断言
var (
	_ sql.Scanner   = (*NullInt64)(nil)
	_ driver.Valuer = NullInt64{}
	_ sql.Scanner   = (*NullString)(nil)
	_ driver.Valuer = NullString{}
)
