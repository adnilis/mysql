package plugins

import (
	"time"

	"github.com/go-sql-driver/mysql"
)

// buildDSN 根据配置构建 MySQL DSN 连接字符串
//
// 应用以下配置到 mysql.Config:
//   - ConnTimeout  → Timeout    (建立 TCP 连接的超时)
//   - ReadTimeout  → ReadTimeout (I/O 读超时)
//   - WriteTimeout → WriteTimeout (I/O 写超时)
//
// 其他字段(Addr/User/Password/DBName/ParseTime/Loc 等)直接转发
func buildDSN(cfg *MySQLPluginConfig) string {
	loc := cfg.Loc
	if loc == "" {
		loc = defaultMySQLLoc
	}

	// 处理 timezone 加载错误，使用默认时区
	location := time.Local
	if l, err := time.LoadLocation(loc); err == nil {
		location = l
	}

	dsnConfig := mysql.Config{
		User:                 cfg.User,
		Passwd:               cfg.Password,
		Net:                  "tcp",
		Addr:                 cfg.Addr,
		DBName:               cfg.DBName,
		ParseTime:            cfg.ParseTime,
		Loc:                  location,
		Timeout:              cfg.ConnTimeout,  // 建立连接超时
		ReadTimeout:          cfg.ReadTimeout,  // 读取超时
		WriteTimeout:         cfg.WriteTimeout, // 写入超时
		AllowNativePasswords: true,
	}
	return dsnConfig.FormatDSN()
}
