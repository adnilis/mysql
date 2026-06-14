package plugins

import "errors"

// 公共 sentinel 错误,推荐用 errors.Is 判定
//
// 使用模式:
//
//	if errors.Is(err, ErrModelNotFound) {
//	    // 记录未命中
//	}
//
// sentinel 错误可被 wrapMySQLError 包装,且包装后仍可被 errors.Is 识别(见 helper.go)。
var (
	// ErrMySQLNotEnabled 插件未启用或未初始化(如 Start 前调用了 DB/Query)
	//
	// 常见触发:在 Start() 之前调用 ORM/Query API
	ErrMySQLNotEnabled = errors.New("mysql is not enabled")

	// ErrModelNotFound 记录不存在
	//
	// 由以下方法在无匹配行时返回:Get / GetByID / First / Take
	// 这些方法返回的错误会通过 wrapMySQLError 包装,errors.Is(err, ErrModelNotFound) 仍为 true
	ErrModelNotFound = errors.New("model not found")

	// ErrInvalidModel 模型无效
	//
	// 触发:Model(nil) / Table("invalid;name") / Save 非指针类型 / 缺失 db 标签
	ErrInvalidModel = errors.New("invalid model")

	// ErrDuplicateKey 主键或唯一索引冲突
	//
	// 注:当前由底层 go-sql-driver 直接返回的 *mysql.MySQLError 携带,
	//     调用方可使用 errors.As 还原后判断 Number == 1062
	ErrDuplicateKey = errors.New("duplicate key")
)

// 内部校验错误,仅插件内部使用,不导出
//
// 这些错误通过 Validate() 与 normalizeMySQLPluginConfig 链路返回,
// 调用方应使用 errors.Is 判定具体原因。
var (
	errMySQLAddrRequired        = errors.New("mysql address is required")
	errMySQLUserRequired        = errors.New("mysql user is required")
	errMySQLDBNameRequired      = errors.New("mysql dbname is required")
	errMySQLPoolSizeInvalid     = errors.New("mysql pool size must be >= 0")
	errMySQLMinIdleConnsInvalid = errors.New("mysql min idle conns must be >= 0")
)
