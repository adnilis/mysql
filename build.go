package plugins

import (
	"fmt"
	"strings"
)

// buildQuery 构建完整的查询语句（包含 JOIN/WHERE/GROUP/HAVING/ORDER 子句）
// 返回构建好的 SQL 语句和参数列表
// 使用缓存机制避免重复构建
func (qr *MySQLQueryResult) buildQuery() (string, []interface{}) {
	// 如果没有修改，直接返回缓存
	if !qr.dirty && qr.preQuery != "" {
		return qr.preQuery, qr.preArgs
	}

	query := qr.query

	// 预计算参数总长度
	totalArgs := len(qr.args) + len(qr.joins)*3 + len(qr.wheres)*2 + len(qr.havings)*2
	allArgs := make([]interface{}, 0, totalArgs)

	// 缓存 ToUpper 结果，避免重复转换
	queryUpper := strings.ToUpper(query)

	// 添加 JOIN 子句
	if len(qr.joins) > 0 {
		fromPos := strings.Index(queryUpper, " "+sqlFrom+" ")
		if fromPos >= 0 {
			insertPos := fromPos + len(" "+sqlFrom+" ")

			// 寻找正确的插入位置 - 寻找 FROM 关键字之后第一个空格的下一个字符
			// 然后检查后续内容是否是关键字
			foundKeyword := false
			for i := fromPos + len(sqlFrom); i < len(query); i++ {
				if query[i] == ' ' {
					remaining := strings.ToUpper(strings.TrimSpace(query[i+1:]))
					if strings.HasPrefix(remaining, sqlWhere) ||
						strings.HasPrefix(remaining, sqlGroupBy) ||
						strings.HasPrefix(remaining, sqlOrderBy) ||
						strings.HasPrefix(remaining, sqlLimit) ||
						strings.HasPrefix(remaining, sqlHaving) {
						insertPos = i + 1
						foundKeyword = true
						break
					}
				}
			}

			// 如果没有找到关键字插入点，说明表名后没有关键字，insertPos 应该指向表名之后
			// 寻找表名结束的位置
			if !foundKeyword {
				foundSpace := false
				for i := insertPos; i < len(query); i++ {
					if query[i] == ' ' {
						insertPos = i
						foundSpace = true
						break
					}
				}
				// 如果没有找到空格（表名在字符串末尾），insertPos 指向字符串末尾
				if !foundSpace {
					insertPos = len(query)
				}
			}

			// 构建 JOIN 子句
			var joinBuilder strings.Builder
			joinBuilder.Grow(len(qr.joins) * 32) // 预分配
			for _, j := range qr.joins {
				joinBuilder.WriteString(" ")
				joinBuilder.WriteString(j.joinType)
				joinBuilder.WriteString(" ")
				joinBuilder.WriteString(j.table)
				if j.on != "" {
					joinBuilder.WriteString(" ON ")
					joinBuilder.WriteString(j.on)
				}
				allArgs = append(allArgs, j.args...)
			}

			query = query[:insertPos] + joinBuilder.String() + query[insertPos:]
		}
	}

	// 添加 WHERE 子句
	if len(qr.wheres) > 0 {
		wherePos := strings.Index(queryUpper, " "+sqlWhere+" ")
		if wherePos < 0 {
			fromPos := strings.Index(queryUpper, " "+sqlFrom+" ")
			if fromPos >= 0 {
				// fromPos 指向 " FROM " 的起始位置
				// 需要找到表名结束的位置，即第一个空白或关键字的位置
				searchStart := fromPos + len(" "+sqlFrom+" ")
				// 寻找 WHERE/GROUP/ORDER/LIMIT 或字符串末尾
				foundPos := len(query)
				for i := searchStart; i < len(query); i++ {
					if query[i] == ' ' || query[i] == '\t' || query[i] == '\n' || query[i] == '\r' {
						// 检查后续内容是否是关键字
						remaining := strings.ToUpper(query[i:])
						if strings.HasPrefix(remaining, sqlWhere) ||
							strings.HasPrefix(remaining, sqlGroupBy) ||
							strings.HasPrefix(remaining, sqlOrderBy) ||
							strings.HasPrefix(remaining, sqlLimit) ||
							strings.HasPrefix(remaining, sqlHaving) ||
							strings.HasPrefix(remaining, sqlOffset) {
							foundPos = i
							break
						}
					}
				}
				wherePos = foundPos
			} else {
				wherePos = len(query)
			}
		}

		var whereBuilder strings.Builder
		whereBuilder.Grow(len(qr.wheres) * 16) // 预分配
		for i, w := range qr.wheres {
			if i == 0 {
				whereBuilder.WriteString(w.condition)
			} else {
				whereBuilder.WriteString(" ")
				whereBuilder.WriteString(w.condition)
			}
			allArgs = append(allArgs, w.args...)
		}

		query = query[:wherePos] + " " + sqlWhere + " " + whereBuilder.String() + query[wherePos:]
	}

	// 添加 GROUP BY 子句
	if len(qr.groups) > 0 {
		groupPos := strings.Index(queryUpper, " "+sqlGroupBy+" ")
		if groupPos < 0 {
			groupPos = strings.Index(queryUpper, " "+sqlOrderBy+" ")
			if groupPos < 0 {
				groupPos = strings.Index(queryUpper, " "+sqlLimit+" ")
				if groupPos < 0 {
					groupPos = len(query)
				}
			}
		}

		groupBy := strings.Join(qr.groups, ", ")
		query = query[:groupPos] + " " + sqlGroupBy + " " + groupBy + query[groupPos:]
	}

	// 添加 HAVING 子句
	if len(qr.havings) > 0 {
		havingPos := strings.Index(queryUpper, " "+sqlHaving+" ")
		if havingPos < 0 {
			havingPos = strings.Index(queryUpper, " "+sqlOrderBy+" ")
			if havingPos < 0 {
				havingPos = strings.Index(queryUpper, " "+sqlLimit+" ")
				if havingPos < 0 {
					havingPos = len(query)
				}
			}
		}

		var havingBuilder strings.Builder
		havingBuilder.Grow(len(qr.havings) * 16) // 预分配
		for i, h := range qr.havings {
			if i == 0 {
				havingBuilder.WriteString(h.condition)
			} else {
				havingBuilder.WriteString(" AND ")
				havingBuilder.WriteString(h.condition)
			}
			allArgs = append(allArgs, h.args...)
		}

		query = query[:havingPos] + " " + sqlHaving + " " + havingBuilder.String() + query[havingPos:]
	}

	// 添加 ORDER BY 子句
	if len(qr.orders) > 0 {
		orderPos := strings.Index(queryUpper, " "+sqlOrderBy+" ")
		if orderPos < 0 {
			orderPos = strings.Index(queryUpper, " "+sqlLimit+" ")
			if orderPos < 0 {
				orderPos = len(query)
			}
		}

		orderBy := strings.Join(qr.orders, ", ")
		query = query[:orderPos] + " " + sqlOrderBy + " " + orderBy + query[orderPos:]
	}

	// 添加 LIMIT 子句
	if qr.limit > 0 {
		limitPos := strings.Index(queryUpper, " "+sqlLimit+" ")
		if limitPos < 0 {
			limitPos = len(query)
		}
		query = query[:limitPos] + fmt.Sprintf(" "+sqlLimit+" %d", qr.limit) + query[limitPos:]
	}

	// 添加 OFFSET 子句
	if qr.offset > 0 {
		offsetPos := strings.Index(queryUpper, " "+sqlOffset+" ")
		if offsetPos < 0 {
			offsetPos = strings.Index(queryUpper, " "+sqlLimit+" ")
			if offsetPos < 0 {
				offsetPos = len(query)
			}
		}
		query = query[:offsetPos] + fmt.Sprintf(" "+sqlOffset+" %d", qr.offset) + query[offsetPos:]
	}

	// 添加原有参数
	allArgs = append(allArgs, qr.args...)

	// 缓存结果
	qr.preQuery = query
	qr.preArgs = allArgs
	qr.dirty = false

	return query, allArgs
}
