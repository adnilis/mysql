package plugins

import (
	"errors"
	"testing"
)

// TestIsValidIdentifier
func TestIsValidIdentifier(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		// Valid
		{"single letter", "a", true},
		{"alphabetic", "abc", true},
		{"underscore prefix", "_x", true},
		{"with underscore", "x_y", true},
		{"with digit", "X1", true},
		{"just underscore", "_", true},
		{"mixed case", "MyTable", true},
		// Invalid
		{"empty", "", false},
		{"digit prefix", "1x", false},
		{"hyphen", "x-y", false},
		{"dot", "x.y", false},
		{"semicolon injection", "x;DROP", false},
		{"space", "x y", false},
		{"quote injection", "x'", false},
		{"sql injection", "users; DROP TABLE users;--", false},
		{"backtick", "`x`", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidIdentifier(tt.in)
			if got != tt.want {
				t.Errorf("isValidIdentifier(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestSanitizeIdentifier
func TestSanitizeIdentifier(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"users", "users"},
		{"  users  ", "users"},
		{"\tusers\t", "users"},
		{"", ""},
		{"   ", ""},
		{"x-y", ""}, // invalid → empty
	}
	for _, tt := range tests {
		got := sanitizeIdentifier(tt.in)
		if got != tt.want {
			t.Errorf("sanitizeIdentifier(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestToSnakeCase 验证 Phase 1.5 算法修复（连续大写不再拆字母）
func TestToSnakeCase(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"UserInfo", "user_info"},
		{"UserID", "user_id"},
		{"User", "user"},
		{"user", "user"},
		{"MySQLPlugin", "my_sql_plugin"},
		{"HTTPResponse", "http_response"},
		{"ID", "id"},
		{"IDS", "ids"},
		{"", ""},
		{"a", "a"},
		{"A", "a"},
		{"ABC", "abc"},
		{"aB", "a_b"},
		{"aBcDef", "a_bc_def"},
	}
	for _, tt := range tests {
		got := toSnakeCase(tt.in)
		if got != tt.want {
			t.Errorf("toSnakeCase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// helperTestModel 用于 getTableNameFromDest
type helperTestModel struct {
	ID int `db:"id"`
}

func (m *helperTestModel) TableName() string { return "helper_models" }

// helperAnon 不实现 IModel
type helperAnon struct {
	Name string
}

// TestGetTableNameFromDest
func TestGetTableNameFromDest(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"IModel struct ptr", &helperTestModel{}, "helper_models"},
		{"IModel slice ptr", &[]helperTestModel{}, "helper_models"},
		{"plain struct ptr falls back to snake+s", &helperAnon{}, "helper_anons"},
		{"plain slice ptr falls back to snake+s", &[]helperAnon{}, "helper_anons"},
		{"non-pointer struct still ok", helperTestModel{}, "helper_models"},
		{"int returns empty", 42, ""},
		{"nil any-typed returns empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// nil 入参会触发 reflect.TypeOf(nil).Kind() 异常，需要先 guard
			if tt.in == nil {
				got := getTableNameFromDest(tt.in)
				if got != tt.want {
					t.Errorf("got %q, want %q", got, tt.want)
				}
				return
			}
			got := getTableNameFromDest(tt.in)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestWrapMySQLError_NilPassthrough
func TestWrapMySQLError_NilPassthrough(t *testing.T) {
	if got := wrapMySQLError("t", "op", nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// TestWrapMySQLError_BasicFormat
func TestWrapMySQLError_BasicFormat(t *testing.T) {
	inner := errors.New("inner reason")
	err := wrapMySQLError("users", "insert", inner)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	want := "users: insert failed: inner reason"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

// TestWrapMySQLError_UnwrapChain
func TestWrapMySQLError_UnwrapChain(t *testing.T) {
	inner := errors.New("inner")
	wrapped := wrapMySQLError("t", "op", inner)
	if !errors.Is(wrapped, inner) {
		t.Error("errors.Is should find inner via Unwrap")
	}

	var target *wrappedMySQLError
	if !errors.As(wrapped, &target) {
		t.Error("errors.As should match *wrappedMySQLError")
	}
	if target.table != "t" {
		t.Errorf("table = %q, want %q", target.table, "t")
	}
	if target.op != "op" {
		t.Errorf("op = %q, want %q", target.op, "op")
	}
}

// TestWrapMySQLError_PreservesSentinels confirms ErrModelNotFound survives wrapping
func TestWrapMySQLError_PreservesSentinels(t *testing.T) {
	wrapped := wrapMySQLError("u", "select", ErrModelNotFound)
	if !errors.Is(wrapped, ErrModelNotFound) {
		t.Error("ErrModelNotFound must be reachable through wrapMySQLError")
	}
}

// TestWrapMySQLError_Is_Method exercises the *wrappedMySQLError.Is short-circuit
func TestWrapMySQLError_Is_Method(t *testing.T) {
	wrapped := wrapMySQLError("users", "select", ErrModelNotFound)

	// 1) 通过 errors.Is 走 Unwrap 链能匹配 sentinel
	if !errors.Is(wrapped, ErrModelNotFound) {
		t.Error("errors.Is should match ErrModelNotFound via Unwrap")
	}

	// 2) Is 方法直接被调用:wrapped.(*wrappedMySQLError).Is(...) 应返回 true
	mErr, ok := wrapped.(*wrappedMySQLError)
	if !ok {
		t.Fatal("wrapMySQLError should return *wrappedMySQLError")
	}
	if !mErr.Is(ErrModelNotFound) {
		t.Error("Is(ErrModelNotFound) should return true")
	}

	// 3) Is(nil) 必须返回 false
	if mErr.Is(nil) {
		t.Error("Is(nil) should return false")
	}

	// 4) Is 对不相关的错误返回 false
	if mErr.Is(ErrInvalidModel) {
		t.Error("Is(ErrInvalidModel) should return false")
	}

	// 5) 双层 wrap 也能找到底层 sentinel
	double := wrapMySQLError("outer", "op", wrapped)
	if !errors.Is(double, ErrModelNotFound) {
		t.Error("errors.Is must traverse double-wrap to find ErrModelNotFound")
	}
}

// TestWrapMySQLError_Accessors covers Table()/Op() for downstream introspection
func TestWrapMySQLError_Accessors(t *testing.T) {
	wrapped := wrapMySQLError("users", "insert", errors.New("dup"))

	mErr, ok := wrapped.(*wrappedMySQLError)
	if !ok {
		t.Fatal("wrapMySQLError should return *wrappedMySQLError")
	}
	if mErr.Table() != "users" {
		t.Errorf("Table() = %q, want %q", mErr.Table(), "users")
	}
	if mErr.Op() != "insert" {
		t.Errorf("Op() = %q, want %q", mErr.Op(), "insert")
	}

	// 空表名也合法
	empty := wrapMySQLError("", "get", errors.New("x"))
	eErr, _ := empty.(*wrappedMySQLError)
	if eErr.Table() != "" {
		t.Errorf("Table() = %q, want empty", eErr.Table())
	}
}

// TestMySQLError_Alias verifies *MySQLError = *wrappedMySQLError (type alias)
func TestMySQLError_Alias(t *testing.T) {
	wrapped := wrapMySQLError("orders", "delete", ErrModelNotFound)

	var mErr *MySQLError
	if !errors.As(wrapped, &mErr) {
		t.Fatal("errors.As should match *MySQLError (type alias)")
	}
	if mErr.Table() != "orders" {
		t.Errorf("via alias Table() = %q, want %q", mErr.Table(), "orders")
	}
	if mErr.Op() != "delete" {
		t.Errorf("via alias Op() = %q, want %q", mErr.Op(), "delete")
	}
}
