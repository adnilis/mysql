package plugins

import (
	"sync"
	"time"
)

// SlowQueryRecord 慢查询样本(R09)
//
// 保留最近 N 条超过 SlowThreshold 的查询,用于 postmortem 分析。
// 调用方通过 QueryLogger.SlowQueryHook 触发本缓冲(由 plugin 内部 wiring),
// 也可由外部代码直接 Record。
type SlowQueryRecord struct {
	Query    string        // 原始 SQL(含占位符)
	Args     []any         // 参数快照(调用方持有,本结构不再读)
	Duration time.Duration // 查询耗时
	Rows     int64         // 受影响/读取行数
	At       time.Time     // 发生时间
}

// SlowQueryBuffer 慢查询环形缓冲(R09)
//
// 容量满后覆盖最旧条目;纯 append-only + 读取快照,无锁路径适合高频写入。
// 大小写敏感:默认 100 条,可通过 NewSlowQueryBuffer 自定义。
type SlowQueryBuffer struct {
	mu   sync.Mutex
	cap  int
	buf  []SlowQueryRecord
	head int // 下一写入位置
	full bool
}

// NewSlowQueryBuffer 创建一个容量为 cap 的环形缓冲
// cap <= 0 时禁用,Record 直接 no-op
func NewSlowQueryBuffer(cap int) *SlowQueryBuffer {
	return &SlowQueryBuffer{cap: cap}
}

// Record 追加一条慢查询样本
func (b *SlowQueryBuffer) Record(query string, args []any, duration time.Duration, rows int64) {
	if b == nil || b.cap <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buf == nil {
		b.buf = make([]SlowQueryRecord, 0, b.cap)
	}
	if len(b.buf) < b.cap {
		b.buf = append(b.buf, SlowQueryRecord{
			Query: query, Args: args, Duration: duration, Rows: rows,
			At: time.Now(),
		})
	} else {
		b.buf[b.head] = SlowQueryRecord{
			Query: query, Args: args, Duration: duration, Rows: rows,
			At: time.Now(),
		}
		b.head = (b.head + 1) % b.cap
		b.full = true
	}
}

// Snapshot 返回当前缓冲内容的快照(按时间倒序:最新在前)
// 容量 0 / 空缓冲返回 nil
func (b *SlowQueryBuffer) Snapshot() []SlowQueryRecord {
	if b == nil || b.cap <= 0 || len(b.buf) == 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(b.buf)
	out := make([]SlowQueryRecord, 0, n)
	if b.full {
		// 从 head(最旧)开始读出顺序,反转得到时间倒序
		for i := 0; i < n; i++ {
			idx := (b.head + i) % n
			out = append(out, b.buf[idx])
		}
		// 上面得到的是"最旧→最新",反转
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	} else {
		// 未满:buf 内 [0, head-1] 已有数据(顺序即插入顺序)
		// 直接反转得到时间倒序
		for i, j := 0, len(b.buf)-1; i < j; i, j = i+1, j-1 {
			b.buf[i], b.buf[j] = b.buf[j], b.buf[i]
		}
		out = append(out, b.buf...)
	}
	return out
}

// Len 返回当前条目数(<= cap)
func (b *SlowQueryBuffer) Len() int {
	if b == nil || b.cap <= 0 {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf)
}

// Reset 清空缓冲
func (b *SlowQueryBuffer) Reset() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.buf = b.buf[:0]
	b.head = 0
	b.full = false
	b.mu.Unlock()
}
