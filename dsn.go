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

	dsnConfig := mysql.Config{
		User:                 cfg.User,
		Passwd:               cfg.Password,
		Net:                  "tcp",
		Addr:                 cfg.Addr,
		DBName:               cfg.DBName,
		ParseTime:            cfg.ParseTime,
		Loc:                  func() *time.Location { l, _ := time.LoadLocation(loc); return l }(),
		AllowNativePasswords: true,
	}
	return dsnConfig.FormatDSN()
}
