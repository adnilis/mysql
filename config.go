package plugins

import (
	"strings"
	"time"
)

// MySQLPluginConfig MySQL 插件配置
type MySQLPluginConfig struct {
	Addr           string        // MySQL 地址，如 "localhost:3306"
	User           string        // MySQL 用户名
	Password       string        // MySQL 密码
	DBName         string        // 数据库名称
	PoolSize       int           // 连接池最大连接数
	MinIdleConns   int           // 连接池最小空闲连接数
	MaxIdleConns   int           // 连接池最大空闲连接数
	MaxLifetime    time.Duration // 连接最大生命周期，0 表示无限制
	MaxIdleTime    time.Duration // 空闲连接最大存活时间
	ConnTimeout    time.Duration // 连接超时时间
	ReadTimeout    time.Duration // 读取超时时间
	WriteTimeout   time.Duration // 写入超时时间
	ParseTime      bool          // 是否解析时间字段为 time.Time
	Loc            string        // 时区位置，如 "Local"、"Asia/Shanghai"
	EnableQueryLog bool          // 是否启用 SQL 查询日志（INSERT/UPDATE/DELETE/SELECT 等）
	SlowThreshold  int64         // 慢查询阈值（毫秒），0 表示不启用
}

// 默认配置值
const (
	defaultMySQLAddr          = "localhost:3306" // 默认地址
	defaultMySQLPoolSize      = 10               // 默认连接池大小
	defaultMySQLMinIdleConns  = 3                // 默认最小空闲连接数
	defaultMySQLMaxIdleConns  = 5                // 默认最大空闲连接数
	defaultMySQLMaxLifetime   = 0                // 默认连接生命周期，0 表示无限制
	defaultMySQLMaxIdleTime   = 5 * time.Minute  // 默认空闲连接存活时间
	defaultMySQLConnTimeout   = 5 * time.Second  // 默认连接超时
	defaultMySQLReadTimeout   = 3 * time.Second  // 默认读取超时
	defaultMySQLWriteTimeout  = 3 * time.Second  // 默认写入超时
	defaultMySQLParseTime     = true             // 默认解析时间
	defaultMySQLLoc           = "Local"          // 默认时区
	defaultMySQLSlowThreshold = int64(0)         // 默认慢查询阈值，0 表示禁用
)

// normalizeMySQLPluginConfig 标准化配置，使用默认值填充零值字段
// 注意：
//   - 必填字段（User/Password/DBName）不在此处填默认，由 Validate 拦截
//   - bool 字段（ParseTime/EnableQueryLog）无法用零值区分"未设置"和"显式 false"，
//     约定为：用户传入的 bool 永远生效（ParseTime 默认为 true，仅当用户传 nil 配置时生效）
func normalizeMySQLPluginConfig(cfg *MySQLPluginConfig) MySQLPluginConfig {
	config := MySQLPluginConfig{
		Addr:          defaultMySQLAddr,
		PoolSize:      defaultMySQLPoolSize,
		MinIdleConns:  defaultMySQLMinIdleConns,
		MaxIdleConns:  defaultMySQLMaxIdleConns,
		MaxLifetime:   defaultMySQLMaxLifetime,
		MaxIdleTime:   defaultMySQLMaxIdleTime,
		ConnTimeout:   defaultMySQLConnTimeout,
		ReadTimeout:   defaultMySQLReadTimeout,
		WriteTimeout:  defaultMySQLWriteTimeout,
		ParseTime:     defaultMySQLParseTime,
		Loc:           defaultMySQLLoc,
		SlowThreshold: defaultMySQLSlowThreshold,
	}

	if cfg == nil {
		return config
	}

	// 只填充非零值，保留用户显式设置的值
	if cfg.Addr != "" {
		config.Addr = cfg.Addr
	}
	if cfg.User != "" {
		config.User = cfg.User
	}
	if cfg.Password != "" {
		config.Password = cfg.Password
	}
	if cfg.DBName != "" {
		config.DBName = cfg.DBName
	}
	if cfg.PoolSize > 0 {
		config.PoolSize = cfg.PoolSize
	}
	if cfg.MinIdleConns >= 0 {
		config.MinIdleConns = cfg.MinIdleConns
	}
	if cfg.MaxIdleConns >= 0 {
		config.MaxIdleConns = cfg.MaxIdleConns
	}
	if cfg.MaxLifetime > 0 {
		config.MaxLifetime = cfg.MaxLifetime
	}
	if cfg.MaxIdleTime > 0 {
		config.MaxIdleTime = cfg.MaxIdleTime
	}
	if cfg.ConnTimeout > 0 {
		config.ConnTimeout = cfg.ConnTimeout
	}
	if cfg.ReadTimeout > 0 {
		config.ReadTimeout = cfg.ReadTimeout
	}
	if cfg.WriteTimeout > 0 {
		config.WriteTimeout = cfg.WriteTimeout
	}
	// bool 字段：用户显式传值直接生效
	config.ParseTime = cfg.ParseTime
	config.EnableQueryLog = cfg.EnableQueryLog
	if cfg.Loc != "" {
		config.Loc = cfg.Loc
	}
	if cfg.SlowThreshold > 0 {
		config.SlowThreshold = cfg.SlowThreshold
	}

	return config
}

// Validate 验证配置的有效性
// 校验项：Addr / User / DBName 必填；PoolSize 与 MinIdleConns 不能为负数
// 不校验：MaxIdleConns / MaxLifetime / MaxIdleTime / 超时类（业务可选）
func (c MySQLPluginConfig) Validate() error {
	if strings.TrimSpace(c.Addr) == "" {
		return errMySQLAddrRequired
	}
	if strings.TrimSpace(c.User) == "" {
		return errMySQLUserRequired
	}
	if strings.TrimSpace(c.DBName) == "" {
		return errMySQLDBNameRequired
	}
	if c.PoolSize < 0 {
		return errMySQLPoolSizeInvalid
	}
	if c.MinIdleConns < 0 {
		return errMySQLMinIdleConnsInvalid
	}
	return nil
}
