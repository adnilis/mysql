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
