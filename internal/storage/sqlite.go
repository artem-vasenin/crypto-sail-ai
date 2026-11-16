// Package storage implements a small SQLite repository using the system libsqlite3.
// CGO_ENABLED=1 and libsqlite3-dev are required. No network Go dependencies.
package storage

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
static int bind_txt(sqlite3_stmt *stmt, int idx, const char *v) {
    return sqlite3_bind_text(stmt, idx, v, -1, SQLITE_TRANSIENT);
}
*/
import "C"

import (
	"crypto-sail-ai/internal/watch"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
	"unsafe"
)

type DB struct {
	mu   sync.Mutex
	db   *C.sqlite3
	path string
}

func Open(path string) (*DB, error) {
	if path == "" || path == ":memory:" {
		return nil, errors.New("file path required; in-memory DB disabled")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// SQLite would otherwise inherit a possibly permissive process umask.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	var ptr *C.sqlite3
	code := C.sqlite3_open_v2(cp, &ptr, C.SQLITE_OPEN_READWRITE|C.SQLITE_OPEN_CREATE|C.SQLITE_OPEN_FULLMUTEX, nil)
	if code != C.SQLITE_OK {
		msg := "sqlite open failed"
		if ptr != nil {
			msg = C.GoString(C.sqlite3_errmsg(ptr))
			C.sqlite3_close(ptr)
		}
		return nil, errors.New(msg)
	}
	s := &DB{db: ptr, path: path}
	C.sqlite3_busy_timeout(ptr, 5000)
	if err := s.exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON;"); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *DB) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	code := C.sqlite3_close(s.db)
	if code != C.SQLITE_OK {
		return fmt.Errorf("sqlite close %d", int(code))
	}
	s.db = nil
	return nil
}
func (s *DB) err() error {
	if s.db == nil {
		return errors.New("database closed")
	}
	return errors.New(C.GoString(C.sqlite3_errmsg(s.db)))
}
func (s *DB) exec(sql string) error { s.mu.Lock(); defer s.mu.Unlock(); return s.execLocked(sql) }
func (s *DB) execLocked(sql string) error {
	if s.db == nil {
		return errors.New("db closed")
	}
	cs := C.CString(sql)
	defer C.free(unsafe.Pointer(cs))
	var errMsg *C.char
	code := C.sqlite3_exec(s.db, cs, nil, nil, &errMsg)
	if code != C.SQLITE_OK {
		if errMsg != nil {
			e := errors.New(C.GoString(errMsg))
			C.sqlite3_free(unsafe.Pointer(errMsg))
			return e
		}
		return s.err()
	}
	return nil
}
func (s *DB) migrate() error {
	return s.exec(`BEGIN IMMEDIATE;
 CREATE TABLE IF NOT EXISTS snapshots(id TEXT PRIMARY KEY,kind TEXT NOT NULL,symbol TEXT NOT NULL,at_ms INTEGER NOT NULL,payload TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS idx_snapshots_kind_time ON snapshots(kind,at_ms DESC);
 CREATE TABLE IF NOT EXISTS decisions(id TEXT PRIMARY KEY,snapshot_id TEXT NOT NULL,phase TEXT NOT NULL,symbol TEXT NOT NULL,status TEXT NOT NULL,at_ms INTEGER NOT NULL,payload TEXT NOT NULL,FOREIGN KEY(snapshot_id) REFERENCES snapshots(id));
 CREATE INDEX IF NOT EXISTS idx_decisions_phase_time ON decisions(phase,at_ms DESC);
 CREATE TABLE IF NOT EXISTS llm_calls(id TEXT PRIMARY KEY,snapshot_id TEXT NOT NULL,phase TEXT NOT NULL,model TEXT NOT NULL,at_ms INTEGER NOT NULL,ok INTEGER NOT NULL,latency_ms INTEGER NOT NULL,prompt_tokens INTEGER NOT NULL,completion_tokens INTEGER NOT NULL,raw TEXT NOT NULL,error TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS watch_ideas(symbol TEXT NOT NULL,side TEXT NOT NULL,at_ms INTEGER NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(symbol,side));
 CREATE TABLE IF NOT EXISTS virtual_positions(id TEXT PRIMARY KEY,at_ms INTEGER NOT NULL,payload TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS position_events(id TEXT PRIMARY KEY,position_id TEXT NOT NULL,at_ms INTEGER NOT NULL,payload TEXT NOT NULL,FOREIGN KEY(position_id) REFERENCES virtual_positions(id));
 CREATE TABLE IF NOT EXISTS post_exit_checkpoints(id TEXT PRIMARY KEY,position_id TEXT NOT NULL,at_ms INTEGER NOT NULL,payload TEXT NOT NULL,FOREIGN KEY(position_id) REFERENCES virtual_positions(id));
 PRAGMA user_version=1;
 COMMIT;`)
}
func (s *DB) rows(query string, args ...string) ([][]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("db closed")
	}
	cs := C.CString(query)
	defer C.free(unsafe.Pointer(cs))
	var st *C.sqlite3_stmt
	if C.sqlite3_prepare_v2(s.db, cs, -1, &st, nil) != C.SQLITE_OK {
		return nil, s.err()
	}
	defer C.sqlite3_finalize(st)
	for i, arg := range args {
		v := C.CString(arg)
		code := C.bind_txt(st, C.int(i+1), v)
		C.free(unsafe.Pointer(v))
		if code != C.SQLITE_OK {
			return nil, s.err()
		}
	}
	var out [][]string
	for {
		code := C.sqlite3_step(st)
		if code == C.SQLITE_DONE {
			break
		}
		if code != C.SQLITE_ROW {
			return nil, s.err()
		}
		n := int(C.sqlite3_column_count(st))
		row := make([]string, n)
		for i := 0; i < n; i++ {
			col := C.sqlite3_column_text(st, C.int(i))
			if col != nil {
				row[i] = C.GoString((*C.char)(unsafe.Pointer(col)))
			}
		}
		out = append(out, row)
	}
	return out, nil
}
func (s *DB) write(query string, args ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("db closed")
	}
	cs := C.CString(query)
	defer C.free(unsafe.Pointer(cs))
	var st *C.sqlite3_stmt
	if C.sqlite3_prepare_v2(s.db, cs, -1, &st, nil) != C.SQLITE_OK {
		return s.err()
	}
	defer C.sqlite3_finalize(st)
	for i, v := range args {
		cv := C.CString(v)
		code := C.bind_txt(st, C.int(i+1), cv)
		C.free(unsafe.Pointer(cv))
		if code != C.SQLITE_OK {
			return s.err()
		}
	}
	if C.sqlite3_step(st) != C.SQLITE_DONE {
		return s.err()
	}
	return nil
}
func id() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func ms(t time.Time) string { return strconv.FormatInt(t.UTC().UnixMilli(), 10) }
func payload(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	return string(b), nil
}
func (s *DB) SaveSnapshot(snapshotID, kind, symbol string, at time.Time, value any) error {
	if snapshotID == "" || kind == "" || symbol == "" || at.IsZero() {
		return errors.New("incomplete snapshot metadata")
	}
	p, e := payload(value)
	if e != nil {
		return e
	}
	return s.write("INSERT INTO snapshots(id,kind,symbol,at_ms,payload) VALUES(?,?,?,?,?)", snapshotID, kind, symbol, ms(at), p)
}
func (s *DB) SaveDecision(snapshotID, phase, symbol, status string, at time.Time, value any) error {
	if snapshotID == "" || phase == "" || symbol == "" || status == "" {
		return errors.New("incomplete decision metadata")
	}
	p, e := payload(value)
	if e != nil {
		return e
	}
	return s.write("INSERT INTO decisions(id,snapshot_id,phase,symbol,status,at_ms,payload) VALUES(?,?,?,?,?,?,?)", id(), snapshotID, phase, symbol, status, ms(at), p)
}
func (s *DB) SaveLLM(snapshotID, phase, model string, ok bool, latency, promptTokens, completionTokens int, raw, errText string) error {
	v := "0"
	if ok {
		v = "1"
	}
	return s.write("INSERT INTO llm_calls(id,snapshot_id,phase,model,at_ms,ok,latency_ms,prompt_tokens,completion_tokens,raw,error) VALUES(?,?,?,?,?,?,?,?,?,?,?)", id(), snapshotID, phase, model, ms(time.Now()), v, strconv.Itoa(latency), strconv.Itoa(promptTokens), strconv.Itoa(completionTokens), raw, errText)
}
func (s *DB) UpsertIdea(idea watch.Idea) error {
	if e := idea.Validate(); e != nil {
		return e
	}
	p, e := payload(idea)
	if e != nil {
		return e
	}
	return s.write("INSERT INTO watch_ideas(symbol,side,at_ms,payload) VALUES(?,?,?,?) ON CONFLICT(symbol,side) DO UPDATE SET at_ms=excluded.at_ms,payload=excluded.payload", idea.Symbol, idea.Side, ms(time.Now()), p)
}
func (s *DB) Ideas() ([]watch.Idea, error) {
	rows, e := s.rows("SELECT payload FROM watch_ideas ORDER BY symbol,side")
	if e != nil {
		return nil, e
	}
	var out []watch.Idea
	for _, r := range rows {
		var x watch.Idea
		if e = json.Unmarshal([]byte(r[0]), &x); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, nil
}
func (s *DB) SavePosition(positionID string, value any) error {
	if positionID == "" {
		return errors.New("missing position id")
	}
	p, e := payload(value)
	if e != nil {
		return e
	}
	return s.write("INSERT INTO virtual_positions(id,at_ms,payload) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET at_ms=excluded.at_ms,payload=excluded.payload", positionID, ms(time.Now()), p)
}
func (s *DB) SavePositionEvent(positionID string, at time.Time, value any) error {
	p, e := payload(value)
	if e != nil {
		return e
	}
	return s.write("INSERT INTO position_events(id,position_id,at_ms,payload) VALUES(?,?,?,?)", id(), positionID, ms(at), p)
}
func (s *DB) Latest(kind string, limit int) ([]json.RawMessage, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("invalid limit")
	}
	rows, e := s.rows("SELECT payload FROM snapshots WHERE kind=? ORDER BY at_ms DESC LIMIT ?", kind, strconv.Itoa(limit))
	if e != nil {
		return nil, e
	}
	out := make([]json.RawMessage, 0, len(rows))
	for _, r := range rows {
		out = append(out, json.RawMessage(r[0]))
	}
	return out, nil
}
func (s *DB) Counts() (map[string]int, error) {
	out := map[string]int{}
	for _, tb := range []string{"snapshots", "decisions", "llm_calls", "watch_ideas", "virtual_positions", "position_events", "post_exit_checkpoints"} {
		rows, e := s.rows("SELECT COUNT(*) FROM " + tb)
		if e != nil {
			return nil, e
		}
		n, _ := strconv.Atoi(rows[0][0])
		out[tb] = n
	}
	return out, nil
}
func (s *DB) Integrity() error {
	rows, e := s.rows("PRAGMA integrity_check")
	if e != nil {
		return e
	}
	if len(rows) != 1 || rows[0][0] != "ok" {
		return fmt.Errorf("integrity check: %v", rows)
	}
	return nil
}

// Backup uses SQLite's online backup API: a raw cp is unsafe while WAL is active.
func (s *DB) Backup(dest string) error {
	if dest == "" || dest == s.path {
		return errors.New("invalid backup destination")
	}
	if _, e := os.Stat(dest); e == nil {
		return errors.New("backup already exists")
	}
	if e := os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cd := C.CString(dest)
	defer C.free(unsafe.Pointer(cd))
	var d *C.sqlite3
	if C.sqlite3_open(cd, &d) != C.SQLITE_OK {
		return errors.New("backup target open failed")
	}
	defer C.sqlite3_close(d)
	main := C.CString("main")
	defer C.free(unsafe.Pointer(main))
	b := C.sqlite3_backup_init(d, main, s.db, main)
	if b == nil {
		return errors.New("sqlite backup init failed")
	}
	code := C.sqlite3_backup_step(b, -1)
	finalCode := C.sqlite3_backup_finish(b)
	if code != C.SQLITE_DONE || finalCode != C.SQLITE_OK {
		return fmt.Errorf("sqlite backup failed: %d/%d", int(code), int(finalCode))
	}
	return nil
}
