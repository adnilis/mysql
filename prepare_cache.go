package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"github.com/jmoiron/sqlx"
)

// PrepareCache 预编译语句 LRU 缓存(R08)
//
// sqlx 没有内置 stmt 缓存;同一条 SQL 在高 QPS 场景会被 driver 反复
// Prepare/Close,产生额外网络往返与 CPU 损耗。本缓存以 SQL 字符串为键,
// 复用 *sqlx.Stmt,显著降低 MySQL 的 parse/plan 开销。
//
// 用法:
//
//	cache := plugin.NewPrepareCache(128)  // LRU 容量 128 条
//	stmt, err := cache.Prepare(ctx, db, "SELECT * FROM users WHERE id = ?")
//	if err != nil { return err }
//	defer stmt.Close()  // 缓存自身不 Close,只在 Stop 时统一释放
//
// 设计取舍:
//   - 不用 sync.Map 是因为它没有 LRU 淘汰;此处用 map+双向链表手动 LRU
//   - 容量 0 表示禁用缓存,每次都重新 Prepare
//   - 缓存的 stmt 生命周期与 plugin 绑定(Stop 时 CloseAll)
type PrepareCache struct {
	mu    sync.Mutex
	cap   int
	items map[string]*prepareCacheEntry
	// 双向链表用于 LRU 淘汰(头=最近使用,尾=最久未用)
	head *prepareCacheEntry
	tail *prepareCacheEntry
	// 命中率指标
	hits   uint64
	misses uint64
}

type prepareCacheEntry struct {
	key  string
	stmt *sqlx.Stmt
	prev *prepareCacheEntry
	next *prepareCacheEntry
}

// NewPrepareCache 创建一个容量为 cap 的 LRU 缓存
// cap <= 0 时缓存禁用,Prepare 总是返回新 stmt
func NewPrepareCache(cap int) *PrepareCache {
	return &PrepareCache{
		cap:   cap,
		items: make(map[string]*prepareCacheEntry),
	}
}

// Prepare 获取或创建一个 prepared statement
// 同 key 命中返回缓存的 *sqlx.Stmt;未命中则 db.Preparex 并 LRU 插入
func (c *PrepareCache) Prepare(ctx context.Context, db *sqlx.DB, query string) (*sqlx.Stmt, error) {
	if c == nil || c.cap <= 0 {
		return db.PreparexContext(ctx, query)
	}

	key := hashQuery(query)

	c.mu.Lock()
	if e, ok := c.items[key]; ok {
		c.hits++
		c.touch(e)
		c.mu.Unlock()
		return e.stmt, nil
	}
	c.mu.Unlock()

	// 未命中:在锁外 Prepare(可能阻塞),再回插
	stmt, err := db.PreparexContext(ctx, query)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.misses++
	// 双重检查:期间可能另一 goroutine 已插入
	if e, ok := c.items[key]; ok {
		// 关闭新 stmt(并发场景),复用已有的
		_ = stmt.Close()
		c.touch(e)
		c.mu.Unlock()
		return e.stmt, nil
	}
	e := &prepareCacheEntry{key: key, stmt: stmt}
	c.items[key] = e
	c.touch(e)
	// 容量超出:淘汰尾部
	if len(c.items) > c.cap {
		old := c.tail
		if old != nil {
			c.unlink(old)
			delete(c.items, old.key)
			_ = old.stmt.Close()
		}
	}
	c.mu.Unlock()
	return stmt, nil
}

// CloseAll 关闭所有缓存的 stmt(Stop 时调用)
func (c *PrepareCache) CloseAll() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var firstErr error
	for k, e := range c.items {
		if err := e.stmt.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(c.items, k)
	}
	c.head, c.tail = nil, nil
	return firstErr
}

// Stats 返回命中率指标
func (c *PrepareCache) Stats() (hits, misses uint64, size int) {
	if c == nil {
		return 0, 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses, len(c.items)
}

// touch 把 entry 移到链表头
func (c *PrepareCache) touch(e *prepareCacheEntry) {
	if c.head == e {
		return
	}
	c.unlink(e)
	e.prev = nil
	e.next = c.head
	if c.head != nil {
		c.head.prev = e
	}
	c.head = e
	if c.tail == nil {
		c.tail = e
	}
}

// unlink 从链表中移除
func (c *PrepareCache) unlink(e *prepareCacheEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else if c.head == e {
		c.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else if c.tail == e {
		c.tail = e.prev
	}
	e.prev, e.next = nil, nil
}

// hashQuery 用 SHA256 哈希 SQL 作为缓存 key
// 比直接用 SQL 字符串作 key 节省 map 内部 hash 比较开销
func hashQuery(q string) string {
	sum := sha256.Sum256([]byte(q))
	return hex.EncodeToString(sum[:8]) // 64-bit 哈希,冲突概率极低
}
