package llm

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	dbMu    sync.Mutex
	dbCache = map[string]*sql.DB{}
)

// sqlDB 是 *sql.DB 的别名，便于在插件逻辑中签名简洁。
type sqlDB = sql.DB

// openDB 按路径复用连接；路径为空时落到用户缓存目录。
func openDB(cfg config) (*sql.DB, error) {
	path := cfg.DatabasePath
	if path == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		path = filepath.Join(base, "ShizukuBot", "llm", "llm.db")
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db, ok := dbCache[path]; ok {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	dbCache[path] = db
	return db, nil
}

func initSchema(db *sql.DB) error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			gid TEXT NOT NULL,
			uid TEXT NOT NULL,
			nickname TEXT NOT NULL,
			content TEXT NOT NULL,
			ts INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_gid_id ON messages(gid, id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_ts ON messages(ts)`,
		`CREATE TABLE IF NOT EXISTS contexts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conv TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			ts INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_contexts_conv_id ON contexts(conv, id)`,
		`CREATE INDEX IF NOT EXISTS idx_contexts_ts ON contexts(ts)`,
	}
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("init schema: %w", err)
		}
	}
	return nil
}

type storedMessage struct {
	ID       int64
	GID      string
	UID      string
	Nickname string
	Content  string
	TS       int64
}

func insertMessage(ctx context.Context, db *sql.DB, msg storedMessage) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO messages(gid, uid, nickname, content, ts) VALUES(?, ?, ?, ?, ?)`,
		msg.GID, msg.UID, msg.Nickname, msg.Content, msg.TS)
	return err
}

// recentMessages 返回最近 limit 条消息（按时间正序）。
func recentMessages(ctx context.Context, db *sql.DB, gid string, limit int) ([]storedMessage, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, gid, uid, nickname, content, ts FROM messages WHERE gid = ? ORDER BY id DESC LIMIT ?`,
		gid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storedMessage
	for rows.Next() {
		var msg storedMessage
		if err := rows.Scan(&msg.ID, &msg.GID, &msg.UID, &msg.Nickname, &msg.Content, &msg.TS); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func insertContext(ctx context.Context, db *sql.DB, conv, role, content string, ts int64) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO contexts(conv, role, content, ts) VALUES(?, ?, ?, ?)`, conv, role, content, ts)
	return err
}

type contextMessage struct {
	Role    string
	Content string
}

// recentContext 返回保留期内的最近 limit 条对话（按时间正序）。
func recentContext(ctx context.Context, db *sql.DB, conv string, limit int, cutoff int64) ([]contextMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx,
		`SELECT role, content FROM contexts WHERE conv = ? AND ts >= ? ORDER BY id DESC LIMIT ?`,
		conv, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contextMessage
	for rows.Next() {
		var msg contextMessage
		if err := rows.Scan(&msg.Role, &msg.Content); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// purge 清理超过保留期的群消息与对话上下文。
func purge(ctx context.Context, db *sql.DB, cutoff int64) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM messages WHERE ts < ?`, cutoff); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM contexts WHERE ts < ?`, cutoff); err != nil {
		return err
	}
	return nil
}

func retentionCutoff(cfg config, now time.Time) int64 {
	hours := cfg.RetentionHours
	if hours <= 0 {
		hours = 24
	}
	return now.Add(-time.Duration(hours) * time.Hour).UnixMilli()
}

// closeCachedDBs 关闭所有缓存的连接；单测在临时目录清理前调用。
func closeCachedDBs() {
	dbMu.Lock()
	defer dbMu.Unlock()
	for _, db := range dbCache {
		_ = db.Close()
	}
	dbCache = map[string]*sql.DB{}
}
