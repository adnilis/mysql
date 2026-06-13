package plugins

import (
	"errors"
	"testing"
	"time"
)

// TestValidate_Required tests that required fields are enforced
func TestValidate_Required(t *testing.T) {
	tests := []struct {
		name    string
		cfg     MySQLPluginConfig
		wantErr error
	}{
		{
			name:    "empty Addr",
			cfg:     MySQLPluginConfig{User: "u", DBName: "d"},
			wantErr: errMySQLAddrRequired,
		},
		{
			name:    "whitespace Addr",
			cfg:     MySQLPluginConfig{Addr: "   ", User: "u", DBName: "d"},
			wantErr: errMySQLAddrRequired,
		},
		{
			name:    "empty User",
			cfg:     MySQLPluginConfig{Addr: "a", DBName: "d"},
			wantErr: errMySQLUserRequired,
		},
		{
			name:    "empty DBName",
			cfg:     MySQLPluginConfig{Addr: "a", User: "u"},
			wantErr: errMySQLDBNameRequired,
		},
		{
			name:    "valid minimal",
			cfg:     MySQLPluginConfig{Addr: "a", User: "u", DBName: "d"},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidate_PoolSizes tests that negative pool sizes are rejected
func TestValidate_PoolSizes(t *testing.T) {
	base := MySQLPluginConfig{Addr: "a", User: "u", DBName: "d"}

	cfg := base
	cfg.PoolSize = -1
	if !errors.Is(cfg.Validate(), errMySQLPoolSizeInvalid) {
		t.Errorf("expected errMySQLPoolSizeInvalid, got %v", cfg.Validate())
	}

	cfg = base
	cfg.MinIdleConns = -1
	if !errors.Is(cfg.Validate(), errMySQLMinIdleConnsInvalid) {
		t.Errorf("expected errMySQLMinIdleConnsInvalid, got %v", cfg.Validate())
	}

	// Zero values should pass
	cfg = base
	cfg.PoolSize = 0
	cfg.MinIdleConns = 0
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected nil for zero pool sizes, got %v", err)
	}
}

// TestNormalize_AllDefaults verifies the nil-input path returns all defaults
func TestNormalize_AllDefaults(t *testing.T) {
	cfg := normalizeMySQLPluginConfig(nil)

	if cfg.Addr != defaultMySQLAddr {
		t.Errorf("Addr default: got %q, want %q", cfg.Addr, defaultMySQLAddr)
	}
	if cfg.PoolSize != defaultMySQLPoolSize {
		t.Errorf("PoolSize default: got %d, want %d", cfg.PoolSize, defaultMySQLPoolSize)
	}
	if cfg.MinIdleConns != defaultMySQLMinIdleConns {
		t.Errorf("MinIdleConns default: got %d, want %d", cfg.MinIdleConns, defaultMySQLMinIdleConns)
	}
	if cfg.MaxIdleConns != defaultMySQLMaxIdleConns {
		t.Errorf("MaxIdleConns default: got %d, want %d", cfg.MaxIdleConns, defaultMySQLMaxIdleConns)
	}
	if cfg.MaxLifetime != defaultMySQLMaxLifetime {
		t.Errorf("MaxLifetime default: got %v, want %v", cfg.MaxLifetime, defaultMySQLMaxLifetime)
	}
	if cfg.MaxIdleTime != defaultMySQLMaxIdleTime {
		t.Errorf("MaxIdleTime default: got %v, want %v", cfg.MaxIdleTime, defaultMySQLMaxIdleTime)
	}
	if cfg.ConnTimeout != defaultMySQLConnTimeout {
		t.Errorf("ConnTimeout default: got %v, want %v", cfg.ConnTimeout, defaultMySQLConnTimeout)
	}
	if cfg.ReadTimeout != defaultMySQLReadTimeout {
		t.Errorf("ReadTimeout default: got %v, want %v", cfg.ReadTimeout, defaultMySQLReadTimeout)
	}
	if cfg.WriteTimeout != defaultMySQLWriteTimeout {
		t.Errorf("WriteTimeout default: got %v, want %v", cfg.WriteTimeout, defaultMySQLWriteTimeout)
	}
	if cfg.ParseTime != defaultMySQLParseTime {
		t.Errorf("ParseTime default: got %v, want %v", cfg.ParseTime, defaultMySQLParseTime)
	}
	if cfg.Loc != defaultMySQLLoc {
		t.Errorf("Loc default: got %q, want %q", cfg.Loc, defaultMySQLLoc)
	}
	if cfg.EnableQueryLog != false {
		t.Errorf("EnableQueryLog default: got %v, want false", cfg.EnableQueryLog)
	}
	if cfg.SlowThreshold != defaultMySQLSlowThreshold {
		t.Errorf("SlowThreshold default: got %d, want %d", cfg.SlowThreshold, defaultMySQLSlowThreshold)
	}
}

// TestNormalize_PartialOverride verifies overridden fields stay, others default
func TestNormalize_PartialOverride(t *testing.T) {
	cfg := normalizeMySQLPluginConfig(&MySQLPluginConfig{
		Addr:         "custom:3306",
		PoolSize:     50,
		MinIdleConns: 7,
		MaxLifetime:  2 * time.Hour,
		WriteTimeout: 10 * time.Second,
	})

	if cfg.Addr != "custom:3306" {
		t.Errorf("Addr override: got %q", cfg.Addr)
	}
	if cfg.PoolSize != 50 {
		t.Errorf("PoolSize override: got %d", cfg.PoolSize)
	}
	if cfg.MinIdleConns != 7 {
		t.Errorf("MinIdleConns override: got %d", cfg.MinIdleConns)
	}
	if cfg.MaxLifetime != 2*time.Hour {
		t.Errorf("MaxLifetime override: got %v", cfg.MaxLifetime)
	}
	if cfg.WriteTimeout != 10*time.Second {
		t.Errorf("WriteTimeout override: got %v", cfg.WriteTimeout)
	}
	// Fields not overridden keep defaults
	if cfg.ConnTimeout != defaultMySQLConnTimeout {
		t.Errorf("ConnTimeout should keep default, got %v", cfg.ConnTimeout)
	}
	if cfg.ReadTimeout != defaultMySQLReadTimeout {
		t.Errorf("ReadTimeout should keep default, got %v", cfg.ReadTimeout)
	}
}

// TestNormalize_ZeroIdleConnsTreatedAsExplicit guards the documented quirk:
// because MinIdleConns/MaxIdleConns use `>= 0` (0 is a valid count),
// an empty literal &MySQLPluginConfig{} cannot express "default" for these fields.
func TestNormalize_ZeroIdleConnsTreatedAsExplicit(t *testing.T) {
	cfg := normalizeMySQLPluginConfig(&MySQLPluginConfig{})
	if cfg.MinIdleConns != 0 {
		t.Errorf("zero MinIdleConns: got %d, want 0 (explicit zero overrides default)", cfg.MinIdleConns)
	}
	if cfg.MaxIdleConns != 0 {
		t.Errorf("zero MaxIdleConns: got %d, want 0", cfg.MaxIdleConns)
	}
	// PoolSize uses `> 0` so 0 falls back to default
	if cfg.PoolSize != defaultMySQLPoolSize {
		t.Errorf("zero PoolSize: got %d, want default %d", cfg.PoolSize, defaultMySQLPoolSize)
	}
}

// TestNormalize_EnableQueryLogPropagated guards against the Debug→EnableQueryLog regression
func TestNormalize_EnableQueryLogPropagated(t *testing.T) {
	cfg := normalizeMySQLPluginConfig(&MySQLPluginConfig{
		EnableQueryLog: true,
	})
	if !cfg.EnableQueryLog {
		t.Error("EnableQueryLog=true was dropped during normalization")
	}

	cfg = normalizeMySQLPluginConfig(&MySQLPluginConfig{
		EnableQueryLog: false,
	})
	if cfg.EnableQueryLog {
		t.Error("EnableQueryLog=false was overridden to true")
	}
}

// TestNormalize_ParseTimeBoolHonoured verifies the bool override
func TestNormalize_ParseTimeBoolHonoured(t *testing.T) {
	// false override should stick
	cfg := normalizeMySQLPluginConfig(&MySQLPluginConfig{
		ParseTime: false,
	})
	if cfg.ParseTime != false {
		t.Errorf("ParseTime=false dropped, got %v", cfg.ParseTime)
	}

	// true override should stick
	cfg = normalizeMySQLPluginConfig(&MySQLPluginConfig{
		ParseTime: true,
	})
	if cfg.ParseTime != true {
		t.Errorf("ParseTime=true dropped, got %v", cfg.ParseTime)
	}
}

// TestNormalize_LocOverride
func TestNormalize_LocOverride(t *testing.T) {
	cfg := normalizeMySQLPluginConfig(&MySQLPluginConfig{
		Loc: "UTC",
	})
	if cfg.Loc != "UTC" {
		t.Errorf("Loc override: got %q", cfg.Loc)
	}
}

// TestNormalize_SlowThresholdOverride
func TestNormalize_SlowThresholdOverride(t *testing.T) {
	cfg := normalizeMySQLPluginConfig(&MySQLPluginConfig{
		SlowThreshold: 500,
	})
	if cfg.SlowThreshold != 500 {
		t.Errorf("SlowThreshold override: got %d", cfg.SlowThreshold)
	}
}

// TestNormalize_NegativeMinIdleNotApplied verifies the >=0 guard
func TestNormalize_NegativeMinIdleNotApplied(t *testing.T) {
	cfg := normalizeMySQLPluginConfig(&MySQLPluginConfig{
		MinIdleConns: -5,
	})
	// Negative is not applied, default stays
	if cfg.MinIdleConns != defaultMySQLMinIdleConns {
		t.Errorf("Negative MinIdleConns leaked through normalization: got %d", cfg.MinIdleConns)
	}
}
