package plugins

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeLoggerConfig 用于测试的 QueryLoggerConfig 实现
type fakeLoggerConfig struct {
	enabled       bool
	slowThreshold int64
}

func (c *fakeLoggerConfig) EnableQueryLog() bool { return c.enabled }
func (c *fakeLoggerConfig) SlowThreshold() int64 { return c.slowThreshold }

// TestNewQueryLogger_NilConfig 传入 nil 不应 panic 且返回 nil
func TestNewQueryLogger_NilConfig(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("NewQueryLogger(nil) panicked: %v", r)
		}
	}()
	ql := NewQueryLogger(nil)
	if ql != nil {
		t.Errorf("expected nil logger for nil config, got %v", ql)
	}
}

// TestNewQueryLogger_Disabled
func TestNewQueryLogger_Disabled(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: false, slowThreshold: 0})
	if ql == nil {
		t.Fatal("expected non-nil logger")
	}
	if ql.IsEnabled() {
		t.Error("expected disabled logger")
	}
	if ql.slowEnabled {
		t.Error("expected slowEnabled=false for threshold=0")
	}
}

// TestNewQueryLogger_Enabled
func TestNewQueryLogger_Enabled(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true, slowThreshold: 100})
	if !ql.IsEnabled() {
		t.Error("expected enabled logger")
	}
	if !ql.slowEnabled {
		t.Error("expected slowEnabled=true for threshold>0")
	}
}

// TestIsEnabled_NilReceiver 验证 nil receiver 不 panic
func TestIsEnabled_NilReceiver(t *testing.T) {
	var ql *QueryLogger
	if ql.IsEnabled() {
		t.Error("nil logger should report disabled")
	}
}

// TestLogQuery_NilReceiver
func TestLogQuery_NilReceiver(t *testing.T) {
	var ql *QueryLogger
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("nil logger LogQuery panicked: %v", r)
		}
	}()
	ql.LogQuery(context.Background(), "SELECT 1", time.Millisecond, 1)
}

// TestLogQuery_DisabledIsNoOp
func TestLogQuery_DisabledIsNoOp(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: false})
	ql.LogQuery(context.Background(), "SELECT 1", time.Millisecond, 1)
	// No panic, no assertion beyond that
}

// TestLogQuery_Enabled
func TestLogQuery_Enabled(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true})
	ql.LogQuery(context.Background(), "SELECT * FROM users WHERE id = ?", time.Millisecond, 1, 42)
}

// TestLogSlowQuery_AboveThreshold
func TestLogSlowQuery_AboveThreshold(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true, slowThreshold: 100})
	ql.LogSlowQuery(context.Background(), "SELECT 1", 200*time.Millisecond, 0)
}

// TestLogSlowQuery_BelowThresholdIsNoOp
func TestLogSlowQuery_BelowThresholdIsNoOp(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true, slowThreshold: 100})
	ql.LogSlowQuery(context.Background(), "SELECT 1", 50*time.Millisecond, 0)
}

// TestLogSlowQuery_NoSlowEnabledIsNoOp
func TestLogSlowQuery_NoSlowEnabledIsNoOp(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true, slowThreshold: 0})
	ql.LogSlowQuery(context.Background(), "SELECT 1", time.Second, 0)
}

// TestLogError_NilLogger
func TestLogError_NilLogger(t *testing.T) {
	var ql *QueryLogger
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("nil logger LogError panicked: %v", r)
		}
	}()
	ql.LogError(context.Background(), "SELECT 1", time.Millisecond, errors.New("boom"))
}

// TestLogError_NilError
func TestLogError_NilError(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true})
	ql.LogError(context.Background(), "SELECT 1", time.Millisecond, nil)
}

// TestLogError_RealError
func TestLogError_RealError(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true})
	ql.LogError(context.Background(), "SELECT * FROM users WHERE x = ?", time.Millisecond, errors.New("syntax error"), "abc")
}

// TestLogTransaction
func TestLogTransaction(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true})
	ql.LogTransaction(context.Background(), "COMMIT", time.Microsecond*500)
}

// TestLogTransaction_Disabled
func TestLogTransaction_Disabled(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: false})
	ql.LogTransaction(context.Background(), "ROLLBACK", time.Microsecond)
}

// TestFormatDuration
func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d        time.Duration
		contains string
	}{
		{0, "0.000ms"},
		{time.Millisecond, "1.000ms"},
		{time.Millisecond + 500*time.Microsecond, "1.500ms"},
		{1500 * time.Millisecond, "1500.000ms"},
	}
	for _, tt := range tests {
		s := formatDuration(tt.d)
		if !strings.Contains(s, tt.contains) {
			t.Errorf("formatDuration(%v) = %q, want contains %q", tt.d, s, tt.contains)
		}
	}
}

// TestFormatQuery_NoArgs
func TestFormatQuery_NoArgs(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true})
	out := ql.formatQuery("SELECT * FROM users")
	if !strings.Contains(out, "`users`") {
		t.Errorf("expected table to be backticked, got %q", out)
	}
}

// TestFormatQuery_StringArg
func TestFormatQuery_StringArg(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true})
	out := ql.formatQuery("SELECT * FROM users WHERE name = ?", "alice")
	if !strings.Contains(out, "'alice'") {
		t.Errorf("expected 'alice' in output, got %q", out)
	}
}

// TestFormatQuery_NilArg
func TestFormatQuery_NilArg(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true})
	out := ql.formatQuery("SELECT ?", nil)
	if !strings.Contains(out, "NULL") {
		t.Errorf("expected NULL in output, got %q", out)
	}
}

// TestFormatQuery_TimeArg
func TestFormatQuery_TimeArg(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true})
	out := ql.formatQuery("SELECT ?", time.Date(2026, 6, 13, 12, 34, 56, 0, time.UTC))
	if !strings.Contains(out, "2026-06-13 12:34:56") {
		t.Errorf("expected formatted time in output, got %q", out)
	}
}

// TestFormatQuery_ByteSliceArg
func TestFormatQuery_ByteSliceArg(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true})
	out := ql.formatQuery("SELECT ?", []byte{0xDE, 0xAD})
	if !strings.Contains(out, "0xdead") {
		t.Errorf("expected hex bytes in output, got %q", out)
	}
}

// TestFormatQuery_BoolArg
func TestFormatQuery_BoolArg(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true})
	out := ql.formatQuery("SELECT ?", true)
	if !strings.Contains(out, "TRUE") {
		t.Errorf("expected TRUE in output, got %q", out)
	}
}

// TestFormatQuery_StringEscape verifies single-quote escaping
func TestFormatQuery_StringEscape(t *testing.T) {
	ql := NewQueryLogger(&fakeLoggerConfig{enabled: true})
	out := ql.formatQuery("SELECT ?", "O'Brien")
	if !strings.Contains(out, "'O''Brien'") {
		t.Errorf("expected escaped quote in output, got %q", out)
	}
}

// TestFormatTableNames_From
func TestFormatTableNames_From(t *testing.T) {
	out := formatTableNames("SELECT * FROM users")
	if !strings.Contains(out, "`users`") {
		t.Errorf("FROM not quoted, got %q", out)
	}
}

// TestFormatTableNames_Join
func TestFormatTableNames_Join(t *testing.T) {
	out := formatTableNames("SELECT * FROM a JOIN b ON a.id = b.id")
	// First FROM table backticked
	if !strings.Contains(out, "`a`") {
		t.Errorf("FROM table not quoted, got %q", out)
	}
	if !strings.Contains(out, "`b`") {
		t.Errorf("JOIN table not quoted, got %q", out)
	}
}

// TestFormatTableNames_Into
func TestFormatTableNames_Into(t *testing.T) {
	out := formatTableNames("INSERT INTO users VALUES (1)")
	if !strings.Contains(out, "`users`") {
		t.Errorf("INTO not quoted, got %q", out)
	}
}

// TestFormatTableNames_Update
func TestFormatTableNames_Update(t *testing.T) {
	out := formatTableNames("UPDATE users SET x = 1")
	if !strings.Contains(out, "`users`") {
		t.Errorf("UPDATE not quoted, got %q", out)
	}
}

// TestFormatTableNames_AlreadyQuotedSkipped
func TestFormatTableNames_AlreadyQuotedSkipped(t *testing.T) {
	in := "SELECT * FROM `users`"
	out := formatTableNames(in)
	// 必须避免双重反引号
	if strings.Contains(out, "``") {
		t.Errorf("already-quoted identifier was double-quoted: %q", out)
	}
}

// TestFormatArg_AllTypes
func TestFormatArg_AllTypes(t *testing.T) {
	tests := []struct {
		name string
		arg  any
		want string
	}{
		{"nil", nil, "NULL"},
		{"string", "abc", "'abc'"},
		{"empty string", "", "''"},
		{"int", 42, "42"},
		{"int64", int64(-7), "-7"},
		{"float", 3.14, "3.14"},
		{"bool true", true, "TRUE"},
		{"bool false", false, "FALSE"},
		{"bytes", []byte{0xCA, 0xFE}, "0xcafe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatArg(tt.arg)
			if got != tt.want {
				t.Errorf("formatArg(%v) = %q, want %q", tt.arg, got, tt.want)
			}
		})
	}
}

// TestFindIdentifierEnd_BasicCases
func TestFindIdentifierEnd_BasicCases(t *testing.T) {
	tests := []struct {
		name  string
		query string
		start int
		want  int
	}{
		{"identifier at end", "abc", 0, 3},
		{"identifier with space", "abc def", 0, 3},
		{"identifier with comma", "abc,def", 0, 3},
		{"identifier with closing paren", "abc)", 0, 3},
		{"start past end", "abc", 3, -1},
		{"subquery start", "  (foo", 0, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findIdentifierEnd(tt.query, tt.start)
			if got != tt.want {
				t.Errorf("findIdentifierEnd(%q, %d) = %d, want %d", tt.query, tt.start, got, tt.want)
			}
		})
	}
}

// TestIsSQLKeywordBoundary
func TestIsSQLKeywordBoundary(t *testing.T) {
	for _, c := range []byte{' ', '\t', '\n', '(', ')', ','} {
		if !isSQLKeywordBoundary(c) {
			t.Errorf("expected %q to be boundary", c)
		}
	}
	for _, c := range []byte{'a', '1', '_', ';'} {
		if isSQLKeywordBoundary(c) {
			t.Errorf("expected %q NOT to be boundary", c)
		}
	}
}
