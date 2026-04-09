package plugins

import "errors"

// MySQL 插件错误定义
var (
	// ErrMySQLNotEnabled MySQL 未启用或未初始化
	ErrMySQLNotEnabled = errors.New("mysql is not enabled")

	// ErrModelNotFound 模型记录不存在
	ErrModelNotFound = errors.New("model not found")

	// ErrInvalidModel 无效的模型
	ErrInvalidModel = errors.New("invalid model")

	// ErrDuplicateKey 重复键错误
	ErrDuplicateKey = errors.New("duplicate key")
)

// 验证错误定义
var (
	errMySQLAddrRequired        = errors.New("mysql address is required")
	errMySQLUserRequired        = errors.New("mysql user is required")
	errMySQLDBNameRequired      = errors.New("mysql dbname is required")
	errMySQLPoolSizeInvalid     = errors.New("mysql pool size must be >= 0")
	errMySQLMinIdleConnsInvalid = errors.New("mysql min idle conns must be >= 0")
)
