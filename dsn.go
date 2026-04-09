package plugins

import (
	"time"

	"github.com/go-sql-driver/mysql"
)

// buildDSN 根据配置构建 MySQL DSN 连接字符串
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
		AllowNativePasswords: true,
	}
	return dsnConfig.FormatDSN()
}
