package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("db path 不能为空")
	}
	if err := ensureDBDir(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("打开 sqlite 失败（path=%q）: %w", path, err)
	}
	// SQLite 并发写入能力有限，单连接最稳妥。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("初始化 sqlite 失败（path=%q）: %w", path, err)
	}
	return s, nil
}

func ensureDBDir(path string) error {
	// 仅支持“文件路径”用法（flag 文案：SQLite数据库文件路径）。
	// 如果未来要支持 SQLite URI/DSN，这里需要更严格的解析。
	raw := path
	if strings.HasPrefix(raw, "file:") {
		raw = strings.TrimPrefix(raw, "file:")
		raw = strings.TrimPrefix(raw, "//")
	}
	raw, _, _ = strings.Cut(raw, "?")
	if raw == "" || raw == ":memory:" {
		return nil
	}

	dir := filepath.Dir(raw)
	if dir == "." || dir == "" || dir == "/" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建数据库目录失败（dir=%q, path=%q）: %w", dir, path, err)
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	// 优先 WAL（并发读友好），但在部分宿主机挂载卷/网络文件系统上可能不支持 WAL/SHM。
	// 此时回退到 DELETE，保证服务能启动。
	if _, err := s.db.ExecContext(ctx, `PRAGMA journal_mode=WAL;`); err != nil {
		if _, err2 := s.db.ExecContext(ctx, `PRAGMA journal_mode=DELETE;`); err2 != nil {
			return err
		}
	}
	stmts := []string{
		`PRAGMA synchronous=NORMAL;`,
		`PRAGMA foreign_keys=ON;`,
		`PRAGMA busy_timeout=5000;`,
		`CREATE TABLE IF NOT EXISTS proxies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			protocol TEXT NOT NULL,
			country_code TEXT NOT NULL,
			ip TEXT NOT NULL,
			port INTEGER NOT NULL,
			last_test_at INTEGER NOT NULL,
			last_test_latency_ms INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(protocol, ip, port)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_proxies_last_test_at ON proxies(last_test_at);`,
		`CREATE TABLE IF NOT EXISTS settings (
			id INTEGER PRIMARY KEY CHECK(id=1),
			json TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		);`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

type Proxy struct {
	ID                int64
	Protocol          string
	CountryCode       string
	IP                string
	Port              int
	LastTestAt        time.Time
	LastTestLatencyMS int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (p Proxy) Addr() string {
	return fmt.Sprintf("%s:%d", p.IP, p.Port)
}

func (s *Store) UpsertUsableProxy(ctx context.Context, p Proxy, testedAt time.Time, latency time.Duration) error {
	if strings.TrimSpace(p.Protocol) == "" || strings.TrimSpace(p.IP) == "" || p.Port <= 0 {
		return fmt.Errorf("proxy 字段不完整")
	}

	now := time.Now().Unix()
	testAt := testedAt.Unix()
	latencyMS := latency.Milliseconds()
	if latencyMS < 0 {
		latencyMS = 0
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO proxies (protocol, country_code, ip, port, last_test_at, last_test_latency_ms, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(protocol, ip, port) DO UPDATE SET
			country_code=excluded.country_code,
			last_test_at=excluded.last_test_at,
			last_test_latency_ms=excluded.last_test_latency_ms,
			updated_at=excluded.updated_at
	`, p.Protocol, p.CountryCode, p.IP, p.Port, testAt, latencyMS, now, now)
	return err
}

func (s *Store) DeleteProxyByID(ctx context.Context, id int64) error {
	if id <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM proxies WHERE id = ?`, id)
	return err
}

func (s *Store) ListProxies(ctx context.Context) ([]Proxy, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, protocol, country_code, ip, port, last_test_at, last_test_latency_ms, created_at, updated_at
		FROM proxies
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out, err := scanProxies(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ListProxiesPage(ctx context.Context, limit int, offset int) ([]Proxy, error) {
	if limit <= 0 {
		return []Proxy{}, nil
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, protocol, country_code, ip, port, last_test_at, last_test_latency_ms, created_at, updated_at
		FROM proxies
		ORDER BY id ASC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out, err := scanProxies(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanProxies(rows *sql.Rows) ([]Proxy, error) {
	var out []Proxy
	for rows.Next() {
		var p Proxy
		var lastTestAt, createdAt, updatedAt int64
		if err := rows.Scan(
			&p.ID,
			&p.Protocol,
			&p.CountryCode,
			&p.IP,
			&p.Port,
			&lastTestAt,
			&p.LastTestLatencyMS,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		p.LastTestAt = time.Unix(lastTestAt, 0)
		p.CreatedAt = time.Unix(createdAt, 0)
		p.UpdatedAt = time.Unix(updatedAt, 0)
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) CountProxies(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM proxies`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) DeleteProxiesByIDs(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	uniq := make(map[int64]struct{}, len(ids))
	filtered := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := uniq[id]; ok {
			continue
		}
		uniq[id] = struct{}{}
		filtered = append(filtered, id)
	}
	if len(filtered) == 0 {
		return 0, nil
	}

	// SQLite 有 max_variable_number 限制（常见 999），因此按批删除，避免一次性 IN (?) 过长导致失败。
	const chunkSize = 900

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}

	var total int64
	for i := 0; i < len(filtered); i += chunkSize {
		end := i + chunkSize
		if end > len(filtered) {
			end = len(filtered)
		}
		chunk := filtered[i:end]

		args := make([]any, 0, len(chunk))
		placeholders := make([]string, 0, len(chunk))
		for _, id := range chunk {
			args = append(args, id)
			placeholders = append(placeholders, "?")
		}

		q := `DELETE FROM proxies WHERE id IN (` + strings.Join(placeholders, ",") + `)`
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			_ = tx.Rollback()
			return 0, err
		}
		n, _ := res.RowsAffected()
		total += n
	}

	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	return total, nil
}

func (s *Store) GetSettingsJSON(ctx context.Context) (string, bool, error) {
	var json string
	err := s.db.QueryRowContext(ctx, `SELECT json FROM settings WHERE id=1`).Scan(&json)
	if err == nil {
		return json, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return "", false, err
}

func (s *Store) UpsertSettingsJSON(ctx context.Context, json string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (id, json, updated_at) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET json=excluded.json, updated_at=excluded.updated_at
	`, json, now)
	return err
}
