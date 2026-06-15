package plugins

import "time"

// MySQLStats MySQL 统计信息
type MySQLStats struct {
	Name              string        // 插件名称
	Addr              string        // MySQL 地址
	DBName            string        // 数据库名称
	State             string        // 当前状态
	PoolSize          int           // 连接池大小
	MinIdleConns      int           // 最小空闲连接数
	MaxIdleConns      int           // 最大空闲连接数
	OpenConnections   int           // 当前打开的连接数
	InUse             int           // 当前使用的连接数
	Idle              int           // 当前空闲的连接数
	WaitCount         int64         // 等待连接的次数
	WaitDuration      time.Duration // 等待连接的总时长
	MaxIdleClosed     int64         // 因空闲超时关闭的连接数
	MaxLifetimeClosed int64         // 因连接超期关闭的连接数
	// R06 内存级查询指标(由 plugin 内部 atomic 计数;通过 Stats() 周期采样导出)
	QueryTotal   int64 // 总查询次数
	QueryErrors  int64 // 查询错误次数
	QuerySlow    int64 // 慢查询次数
	RowsRead     int64 // 总读取行数
	RowsAffected int64 // 总影响行数
}
