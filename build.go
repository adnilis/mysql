package plugins

import (
	"strconv"
	"strings"
)

// edit 子句操作类型
const (
	editJoin = iota
	editWhereInsert
	editWhereAppend
	editGroupInsert
	editGroupAppend
	editHavingInsert
	editHavingAppend
	editOrderInsert
	editOrderAppend
	editLimitInsert
	editLimitReplace
	editOffsetInsert
	editOffsetReplace
)

// edit 表示在原 query 上的一个插入/替换操作
// start == end 表示纯插入（不替换原内容），start < end 表示替换 [start:end) 区间
// 所有 start/end 位置都是原 query 中的绝对位置，不受其他 edit 影响
// 使用 op 字段 + 数据字段的判别式联合，避免 closure 捕获 allArgs 导致堆逃逸
type edit struct {
	start   int
	end     int
	op      int
	joins   []joinClause
	wheres  []whereClause
	groups  []string
	havings []havingClause
	orders  []string
	limit   int
	offset  int
}

// emitEdit 根据 e.op 写入对应子句内容到 b，并收集参数到 allArgs
// 单一函数 + switch 分发，无闭包，所有调用栈可内联
func emitEdit(e *edit, b *strings.Builder, allArgs *[]interface{}) {
	switch e.op {
	case editJoin:
		for _, j := range e.joins {
			b.WriteString(" ")
			b.WriteString(j.joinType)
			b.WriteString(" ")
			b.WriteString(j.table)
			if j.on != "" {
				b.WriteString(" ON ")
				b.WriteString(j.on)
			}
			*allArgs = append(*allArgs, j.args...)
		}
	case editWhereInsert:
		b.WriteString(" ")
		b.WriteString(sqlWhere)
		b.WriteString(" ")
		for i, w := range e.wheres {
			if i == 0 {
				b.WriteString(w.condition)
			} else {
				b.WriteString(" ")
				b.WriteString(w.condition)
			}
			*allArgs = append(*allArgs, w.args...)
		}
	case editWhereAppend:
		b.WriteString(" AND ")
		for i, w := range e.wheres {
			if i == 0 {
				b.WriteString(w.condition)
			} else {
				b.WriteString(" ")
				b.WriteString(w.condition)
			}
			*allArgs = append(*allArgs, w.args...)
		}
	case editGroupInsert:
		b.WriteString(" ")
		b.WriteString(sqlGroupBy)
		b.WriteString(" ")
		b.WriteString(strings.Join(e.groups, ", "))
	case editGroupAppend:
		b.WriteString(", ")
		b.WriteString(strings.Join(e.groups, ", "))
	case editHavingInsert:
		b.WriteString(" ")
		b.WriteString(sqlHaving)
		b.WriteString(" ")
		for i, h := range e.havings {
			if i == 0 {
				b.WriteString(h.condition)
			} else {
				b.WriteString(" AND ")
				b.WriteString(h.condition)
			}
			*allArgs = append(*allArgs, h.args...)
		}
	case editHavingAppend:
		b.WriteString(" AND ")
		for i, h := range e.havings {
			if i == 0 {
				b.WriteString(h.condition)
			} else {
				b.WriteString(" AND ")
				b.WriteString(h.condition)
			}
			*allArgs = append(*allArgs, h.args...)
		}
	case editOrderInsert:
		b.WriteString(" ")
		b.WriteString(sqlOrderBy)
		b.WriteString(" ")
		b.WriteString(strings.Join(e.orders, ", "))
	case editOrderAppend:
		b.WriteString(", ")
		b.WriteString(strings.Join(e.orders, ", "))
	case editLimitInsert:
		b.WriteString(" ")
		b.WriteString(sqlLimit)
		b.WriteString(" ")
		b.WriteString(strconv.Itoa(e.limit))
	case editLimitReplace:
		b.WriteString(strconv.Itoa(e.limit))
	case editOffsetInsert:
		b.WriteString(" ")
		b.WriteString(sqlOffset)
		b.WriteString(" ")
		b.WriteString(strconv.Itoa(e.offset))
	case editOffsetReplace:
		b.WriteString(strconv.Itoa(e.offset))
	}
}

// buildQuery 构建完整的查询语句（包含 JOIN/WHERE/GROUP/HAVING/ORDER/LIMIT/OFFSET 子句）
// 使用单 strings.Builder 拼装，所有位置在原 query 上预计算，避免 N 次字符串拼接
// 天然修复 JOIN+WHERE 等多子句交互时的位置偏移 bug
func (qr *MySQLQueryResult) buildQuery() (string, []interface{}) {
	// 如果没有修改，直接返回缓存
	if !qr.dirty && qr.preQuery != "" {
		return qr.preQuery, qr.preArgs
	}

	query := qr.query
	queryUpper := strings.ToUpper(query)

	// 预估参数总容量
	totalArgs := len(qr.args) + len(qr.joins)*3 + len(qr.wheres)*2 + len(qr.havings)*2
	allArgs := make([]interface{}, 0, totalArgs)

	// 预分配 edits 容量为 7（最多 7 个子句类型：JOIN/WHERE/GROUP/HAVING/ORDER/LIMIT/OFFSET）
	// 避免 append 扩容时复制大量数据
	edits := make([]edit, 0, 7)

	// ---------- JOIN：始终在 FROM 表名后插入 ----------
	if len(qr.joins) > 0 {
		fromPos := strings.Index(queryUpper, " "+sqlFrom+" ")
		if fromPos >= 0 {
			insertPos := fromPos + len(" "+sqlFrom+" ")
			// 找表名结束位置（下一个空格）
			foundSpace := false
			for i := insertPos; i < len(query); i++ {
				if query[i] == ' ' {
					insertPos = i
					foundSpace = true
					break
				}
			}
			if !foundSpace {
				insertPos = len(query)
			}

			edits = append(edits, edit{
				start: insertPos,
				end:   insertPos,
				op:    editJoin,
				joins: qr.joins,
			})
		}
	}

	// ---------- WHERE ----------
	if len(qr.wheres) > 0 {
		wherePos := strings.Index(queryUpper, " "+sqlWhere+" ")
		if wherePos < 0 {
			// 无现有 WHERE：在 FROM 表名后（或末尾）插入新 WHERE
			// 默认在末尾插入；只有找到表名后的关键字位置时才覆盖
			insertPos := len(query)
			fromPos := strings.Index(queryUpper, " "+sqlFrom+" ")
			if fromPos >= 0 {
				searchStart := fromPos + len(" "+sqlFrom+" ")
				// 找表名后第一个关键字位置
				for i := searchStart; i < len(query); i++ {
					if query[i] == ' ' || query[i] == '\t' || query[i] == '\n' || query[i] == '\r' {
						remaining := strings.ToUpper(query[i:])
						if strings.HasPrefix(remaining, " "+sqlWhere) ||
							strings.HasPrefix(remaining, " "+sqlGroupBy) ||
							strings.HasPrefix(remaining, " "+sqlOrderBy) ||
							strings.HasPrefix(remaining, " "+sqlLimit) ||
							strings.HasPrefix(remaining, " "+sqlHaving) ||
							strings.HasPrefix(remaining, " "+sqlOffset) {
							insertPos = i
							break
						}
					}
				}
			}

			edits = append(edits, edit{
				start:  insertPos,
				end:    insertPos,
				op:     editWhereInsert,
				wheres: qr.wheres,
			})
		} else {
			// 有现有 WHERE：追加 " AND cond1 cond2 ..."
			endPos := len(queryUpper)
			for _, kw := range []string{sqlGroupBy, sqlOrderBy, sqlLimit, sqlHaving, sqlOffset} {
				if pos := strings.Index(queryUpper, " "+kw+" "); pos >= 0 && pos < endPos {
					endPos = pos
				}
			}

			edits = append(edits, edit{
				start:  endPos,
				end:    endPos,
				op:     editWhereAppend,
				wheres: qr.wheres,
			})
		}
	}

	// ---------- GROUP BY ----------
	if len(qr.groups) > 0 {
		if strings.Index(queryUpper, " "+sqlGroupBy+" ") < 0 {
			// 无现有 GROUP BY：在 ORDER BY/HAVING/LIMIT/OFFSET/末尾之前插入
			insertPos := len(query)
			for _, kw := range []string{sqlOrderBy, sqlHaving, sqlLimit, sqlOffset} {
				if pos := strings.Index(queryUpper, " "+kw+" "); pos >= 0 && pos < insertPos {
					insertPos = pos
				}
			}

			edits = append(edits, edit{
				start:  insertPos,
				end:    insertPos,
				op:     editGroupInsert,
				groups: qr.groups,
			})
		} else {
			// 有现有 GROUP BY：追加 ", col1, col2, ..."
			endPos := len(queryUpper)
			for _, kw := range []string{sqlOrderBy, sqlHaving, sqlLimit, sqlOffset} {
				if pos := strings.Index(queryUpper, " "+kw+" "); pos >= 0 && pos < endPos {
					endPos = pos
				}
			}

			edits = append(edits, edit{
				start:  endPos,
				end:    endPos,
				op:     editGroupAppend,
				groups: qr.groups,
			})
		}
	}

	// ---------- HAVING ----------
	if len(qr.havings) > 0 {
		if strings.Index(queryUpper, " "+sqlHaving+" ") < 0 {
			// 无现有 HAVING：在 ORDER BY/LIMIT/OFFSET/末尾之前插入
			insertPos := len(query)
			for _, kw := range []string{sqlOrderBy, sqlLimit, sqlOffset} {
				if pos := strings.Index(queryUpper, " "+kw+" "); pos >= 0 && pos < insertPos {
					insertPos = pos
				}
			}

			edits = append(edits, edit{
				start:   insertPos,
				end:     insertPos,
				op:      editHavingInsert,
				havings: qr.havings,
			})
		} else {
			// 有现有 HAVING：追加 " AND cond1 AND cond2 ..."
			endPos := len(queryUpper)
			for _, kw := range []string{sqlOrderBy, sqlLimit, sqlOffset} {
				if pos := strings.Index(queryUpper, " "+kw+" "); pos >= 0 && pos < endPos {
					endPos = pos
				}
			}

			edits = append(edits, edit{
				start:   endPos,
				end:     endPos,
				op:      editHavingAppend,
				havings: qr.havings,
			})
		}
	}

	// ---------- ORDER BY ----------
	if len(qr.orders) > 0 {
		if strings.Index(queryUpper, " "+sqlOrderBy+" ") < 0 {
			// 无现有 ORDER BY：在 LIMIT/OFFSET/末尾之前插入
			insertPos := len(query)
			for _, kw := range []string{sqlLimit, sqlOffset} {
				if pos := strings.Index(queryUpper, " "+kw+" "); pos >= 0 && pos < insertPos {
					insertPos = pos
				}
			}

			edits = append(edits, edit{
				start:  insertPos,
				end:    insertPos,
				op:     editOrderInsert,
				orders: qr.orders,
			})
		} else {
			// 有现有 ORDER BY：追加 ", col1, col2, ..."
			endPos := len(queryUpper)
			for _, kw := range []string{sqlLimit, sqlOffset} {
				if pos := strings.Index(queryUpper, " "+kw+" "); pos >= 0 && pos < endPos {
					endPos = pos
				}
			}

			edits = append(edits, edit{
				start:  endPos,
				end:    endPos,
				op:     editOrderAppend,
				orders: qr.orders,
			})
		}
	}

	// ---------- LIMIT ----------
	if qr.limit > 0 {
		limitPos := strings.Index(queryUpper, " "+sqlLimit+" ")
		if limitPos < 0 {
			// 无现有 LIMIT：追加到末尾
			edits = append(edits, edit{
				start: len(query),
				end:   len(query),
				op:    editLimitInsert,
				limit: qr.limit,
			})
		} else {
			// 有现有 LIMIT：替换值
			valueStart := limitPos + len(" "+sqlLimit+" ")
			valueEnd := valueStart
			for valueEnd < len(query) && query[valueEnd] >= '0' && query[valueEnd] <= '9' {
				valueEnd++
			}
			edits = append(edits, edit{
				start: valueStart,
				end:   valueEnd,
				op:    editLimitReplace,
				limit: qr.limit,
			})
		}
	}

	// ---------- OFFSET ----------
	if qr.offset > 0 {
		offsetPos := strings.Index(queryUpper, " "+sqlOffset+" ")
		if offsetPos < 0 {
			// 无现有 OFFSET：在 LIMIT/末尾之前插入
			insertPos := len(query)
			if pos := strings.Index(queryUpper, " "+sqlLimit+" "); pos >= 0 {
				insertPos = pos
			}

			edits = append(edits, edit{
				start:  insertPos,
				end:    insertPos,
				op:     editOffsetInsert,
				offset: qr.offset,
			})
		} else {
			// 有现有 OFFSET：替换值
			valueStart := offsetPos + len(" "+sqlOffset+" ")
			valueEnd := valueStart
			for valueEnd < len(query) && query[valueEnd] >= '0' && query[valueEnd] <= '9' {
				valueEnd++
			}
			edits = append(edits, edit{
				start:  valueStart,
				end:    valueEnd,
				op:     editOffsetReplace,
				offset: qr.offset,
			})
		}
	}

	// 按 start 位置排序所有 edit（插入排序，n≤7 时比 sort.Slice 更轻量且无 closure 逃逸）
	for i := 1; i < len(edits); i++ {
		for j := i; j > 0 && edits[j].start < edits[j-1].start; j-- {
			edits[j], edits[j-1] = edits[j-1], edits[j]
		}
	}

	// 用单 strings.Builder 拼装最终 SQL
	// 同时按段写入原 query 时扫描 `?` 并收集对应的 qr.args（按最终 SQL 中 `?` 出现顺序）
	var b strings.Builder
	b.Grow(len(query) + 64)
	qrArgsIdx := 0
	prevEnd := 0
	for i := range edits {
		// 写入原 query 段 [prevEnd, e.start)
		segment := query[prevEnd:edits[i].start]
		b.WriteString(segment)
		for j := 0; j < len(segment); j++ {
			if segment[j] == '?' && qrArgsIdx < len(qr.args) {
				allArgs = append(allArgs, qr.args[qrArgsIdx])
				qrArgsIdx++
			}
		}
		// 写入子句内容
		emitEdit(&edits[i], &b, &allArgs)
		prevEnd = edits[i].end
	}
	// 写入最后一段原 query
	segment := query[prevEnd:]
	b.WriteString(segment)
	for j := 0; j < len(segment); j++ {
		if segment[j] == '?' && qrArgsIdx < len(qr.args) {
			allArgs = append(allArgs, qr.args[qrArgsIdx])
			qrArgsIdx++
		}
	}

	// 缓存结果
	qr.preQuery = b.String()
	qr.preArgs = allArgs
	qr.dirty = false

	return qr.preQuery, qr.preArgs
}
