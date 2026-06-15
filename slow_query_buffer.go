package plugins

import (
	"sync"
	"sync/atomic"
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
// 容量满后覆盖最旧条目;纯 append-only + 读取快照,R11-perf 优化:
//   - 写入用 mutex 保护(快速临界区)
//   - 读取用 atomic 快照 head/full(完全无锁)
//   - 快照后单次反序(避免读路径持锁 O(n))
type SlowQueryBuffer struct {
	mu       sync.Mutex
	cap      int
	buf      []SlowQueryRecord
	head     int64 // atomic:下一写入位置(读路径无锁读)
	fullFlag int32 // atomic:是否已满(读路径无锁读)
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
	// R11-perf:用 atomic.Store 暴露 head/full 给读路径
	if len(b.buf) < b.cap {
		b.buf = append(b.buf, SlowQueryRecord{
			Query: query, Args: args, Duration: duration, Rows: rows,
			At: time.Now(),
		})
	} else {
		// 已满:head 为下一覆盖位置(最旧)
		head := int(atomic.LoadInt64(&b.head))
		b.buf[head] = SlowQueryRecord{
			Query: query, Args: args, Duration: duration, Rows: rows,
			At: time.Now(),
		}
		newHead := int64((head + 1) % b.cap)
		atomic.StoreInt64(&b.head, newHead)
		atomic.StoreInt32(&b.fullFlag, 1)
	}
}

// Snapshot 返回当前缓冲内容的快照(按时间倒序:最新在前)
// R11-perf:无锁读路径 — atomic.Load 拿 head/full 后,临界区只用于复制切片
//
// 注意:仍需 b.mu 锁来保证"读取时不会被并发 Reset 清空",但临界区长度
// 从"完整 reverse" 缩短为"memmove",后续 reverse 在锁外完成
func (b *SlowQueryBuffer) Snapshot() []SlowQueryRecord {
	if b == nil || b.cap <= 0 {
		return nil
	}
	// 快速路径:atomic 读 head/full,容量未满且空 → 直接返回
	full := atomic.LoadInt32(&b.fullFlag) == 1
	head := int(atomic.LoadInt64(&b.head))

	b.mu.Lock()
	if len(b.buf) == 0 {
		b.mu.Unlock()
		return nil
	}
	n := len(b.buf)
	// 复制一份 out(锁内,memmove O(n))
	out := make([]SlowQueryRecord, n)
	copy(out, b.buf)
	b.mu.Unlock()

	if full {
		// 环形已满:从 head 位置(最旧)开始,按物理顺序读
		// 然后整体反序得到时间倒序
		rotated := make([]SlowQueryRecord, n)
		for i := 0; i < n; i++ {
			rotated[i] = out[(head+i)%n]
		}
		for i, j := 0, len(rotated)-1; i < j; i, j = i+1, j-1 {
			rotated[i], rotated[j] = rotated[j], rotated[i]
		}
		return rotated
	}
	// 未满:out[0..head) 已有数据,head 前的即为最新;
	// 当前 out 已是 [0]=最旧 [head-1]=最新;反序即可
	for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Len 返回当前条目数(<= cap)
func (b *SlowQueryBuffer) Len() int {
	if b == nil || b.cap <= 0 {
		return 0
	}
	full := atomic.LoadInt32(&b.fullFlag) == 1
	if !full {
		b.mu.Lock()
		l := len(b.buf)
		b.mu.Unlock()
		return l
	}
	return b.cap
}

// Reset 清空缓冲
func (b *SlowQueryBuffer) Reset() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.buf = b.buf[:0]
	b.head = 0
	b.fullFlag = 0
	b.mu.Unlock()
	atomic.StoreInt64(&b.head, 0)
	atomic.StoreInt32(&b.fullFlag, 0)
}
