package plugins

import (
	"strconv"
	"strings"
)

// containsKeywordFold 不分配 ToUpper 副本的 ASCII 关键字存在性检测
// 等价于 strings.Contains(strings.ToUpper(s), kw) 但避免整段拷贝
// 双方均归一到小写后逐字节比对,大小写字母视为相等
//
// 用于 query.go 链式 Update/Select/Distinct/Pluck 等热路径
// 替代原 queryUpper := strings.ToUpper(query) + strings.Contains(queryUpper, "...") 模式
func containsKeywordFold(query, kw string) bool {
	if len(kw) == 0 || len(query) < len(kw) {
		return false
	}
	for i := 0; i+len(kw) <= len(query); i++ {
		match := true
		for j := 0; j < len(kw); j++ {
			q := lowerASCII(query[i+j])
			k := lowerASCII(kw[j])
			if q != k {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// indexKeywordFold 不分配 ToUpper 副本的 ASCII 关键字位置查找
// 等价于 strings.Index(strings.ToUpper(s), kw) 但避免整段拷贝
// 未找到返回 -1
func indexKeywordFold(query, kw string) int {
	if len(kw) == 0 || len(query) < len(kw) {
		return -1
	}
	for i := 0; i+len(kw) <= len(query); i++ {
		match := true
		for j := 0; j < len(kw); j++ {
			q := lowerASCII(query[i+j])
			k := lowerASCII(kw[j])
			if q != k {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// lowerASCII 单字节 ASCII 大写转小写,非字母保持原样
func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 32
	}
	return b
}

// writeWhereJoin 根据 whereOp 写出条件间的连接符(AND/OR/NOT)
// 用作 emitEdit 子调用,避免重复 switch 与字符串字面量散落
func writeWhereJoin(b *strings.Builder, op whereOp) {
	switch op {
	case whereOpOr:
		b.WriteString(" OR ")
	case whereOpNot:
		b.WriteString(" NOT ")
	default:
		b.WriteString(" AND ")
	}
}

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
			if i > 0 {
				writeWhereJoin(b, w.op)
			}
			b.WriteString(w.condition)
			*allArgs = append(*allArgs, w.args...)
		}
	case editWhereAppend:
		// 追加到已有 WHERE:首个条件按其 w.op 派发(默认 AND,Or→OR,Not→NOT),
		// 后续条件也按各自 w.op 派发
		for _, w := range e.wheres {
			writeWhereJoin(b, w.op)
			b.WriteString(w.condition)
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

	// 复用对象池的 scratch 缓冲(由 reset() 截断到 0),消除每次 make 分配
	edits := qr.scratchEdits[:0]
	allArgs := qr.scratchArgs[:0]

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
		found := strings.Contains(queryUpper, " "+sqlWhere+" ")
		if !found {
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
		if !strings.Contains(queryUpper, " "+sqlGroupBy+" ") {
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
		if !strings.Contains(queryUpper, " "+sqlHaving+" ") {
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
		if !strings.Contains(queryUpper, " "+sqlOrderBy+" ") {
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
