package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteSchemaVersion = 2

const (
	sqliteRuntimeID    = 1413960498
	sqliteAttributesID = 1413955890
)

type sqliteStore struct {
	db    *sql.DB
	table string
}

func sqliteFile(path string, create bool) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) && create {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if os.IsExist(err) {
			return sqliteFile(path, false)
		}
		if err != nil {
			return err
		}
		return f.Close()
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("SQLite database must be a regular file, not a symlink")
	}
	if create {
		return os.Chmod(path, 0600)
	}
	return nil
}

func connectSQLite(path string, readOnly bool) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if !readOnly {
		if err := privateDir(filepath.Dir(abs)); err != nil {
			return nil, err
		}
	}
	if err := sqliteFile(abs, !readOnly); err != nil {
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := sqliteFile(abs+suffix, false); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	q := url.Values{"_pragma": {"busy_timeout(10000)"}}
	if readOnly {
		q.Set("mode", "ro")
	} else {
		q.Set("mode", "rw")
		q.Set("_txlock", "immediate")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func checkSQLiteVersion(db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return 0, err
	}
	if version != 0 && version != sqliteSchemaVersion {
		return 0, fmt.Errorf("unsupported SQLite schema version: %d; an explicit offline conversion is required", version)
	}
	return version, nil
}

func openSQLite(path string) (*sqliteStore, error) {
	return openSQLiteRole(path, false)
}

func openStateSQLite(path string) (*sqliteStore, error) {
	return openSQLiteRole(path, true)
}

func openSQLiteRole(path string, runtime bool) (*sqliteStore, error) {
	db, err := connectSQLite(path, false)
	if err != nil {
		return nil, err
	}
	s := &sqliteStore{db: db, table: "properties"}
	if runtime {
		s.table = "state"
	}
	if err := s.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *sqliteStore) initialize() error {
	version, err := checkSQLiteVersion(s.db)
	if err != nil {
		return err
	}
	if err := s.checkRole(version); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if _, err := s.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			return err
		}
	}
	if runtime.GOOS != "windows" {
		if _, err := s.db.Exec("PRAGMA synchronous=FULL"); err != nil {
			return err
		}
	}
	if version == sqliteSchemaVersion {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	schema := `
CREATE TABLE properties (key TEXT PRIMARY KEY, value_json TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS peers (
 peer TEXT PRIMARY KEY, kind TEXT NOT NULL, id INTEGER NOT NULL,
 name TEXT NOT NULL, username TEXT NOT NULL COLLATE NOCASE, is_dialog INTEGER NOT NULL,
 access_hash INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS peers_username ON peers(username);
CREATE TABLE IF NOT EXISTS media (
 peer TEXT NOT NULL, message_id INTEGER NOT NULL, media_id TEXT NOT NULL,
 media_kind TEXT NOT NULL, file_name TEXT NOT NULL, mime_type TEXT NOT NULL,
 size_bytes INTEGER NOT NULL, PRIMARY KEY(peer, message_id)
);
CREATE INDEX IF NOT EXISTS media_identity ON media(media_kind, media_id);
CREATE TABLE IF NOT EXISTS files (
 peer TEXT NOT NULL, message_id INTEGER NOT NULL, media_id TEXT NOT NULL,
 media_kind TEXT NOT NULL, path TEXT NOT NULL, size_bytes INTEGER NOT NULL,
 sha256 TEXT NOT NULL, downloaded_at TEXT NOT NULL,
 PRIMARY KEY(peer, message_id, media_id, media_kind, path)
);
PRAGMA application_id=1413955890;
PRAGMA user_version=2;`
	if s.table == "state" {
		schema = `CREATE TABLE state (key TEXT PRIMARY KEY, value_json TEXT NOT NULL);
PRAGMA application_id=1413960498;
PRAGMA user_version=2;`
	}
	_, err = tx.Exec(schema)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) checkRole(version int) error {
	var appID int
	if err := s.db.QueryRow("PRAGMA application_id").Scan(&appID); err != nil {
		return err
	}
	expected := sqliteAttributesID
	if s.table == "state" {
		expected = sqliteRuntimeID
	}
	if version == 0 {
		var tables int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return err
		}
		if appID != 0 || tables != 0 {
			return errors.New("unversioned SQLite database is not empty; explicit offline conversion required")
		}
		return nil
	}
	if appID != expected {
		return fmt.Errorf("SQLite database role mismatch: expected %s", s.table)
	}
	return nil
}

func (s *sqliteStore) Close() error { return s.db.Close() }

func (s *sqliteStore) ReadJSON(key string, out any) error {
	var value []byte
	err := s.db.QueryRow("SELECT value_json FROM "+s.table+" WHERE key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return os.ErrNotExist
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(value, out); err != nil {
		return fmt.Errorf("decode SQLite state %q: %w", key, err)
	}
	return nil
}

func (s *sqliteStore) WriteJSON(key string, value any) error {
	return s.WriteJSONBatch(map[string]any{key: value}, nil)
}

func (s *sqliteStore) WriteJSONBatch(values map[string]any, deleteKeys []string) error {
	encoded := make(map[string][]byte, len(values))
	for key, value := range values {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		encoded[key] = b
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range encoded {
		if _, err := tx.Exec("INSERT INTO "+s.table+"(key,value_json) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json WHERE value_json<>excluded.value_json", key, string(value)); err != nil {
			return err
		}
	}
	for _, key := range deleteKeys {
		if _, err := tx.Exec("DELETE FROM "+s.table+" WHERE key=?", key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *sqliteStore) UpsertPeer(key string, p PeerInfo, isDialog bool) error {
	if !validPeer.MatchString(key) || key != fmt.Sprintf("%s-%d", p.Kind, p.ID) {
		return errors.New("invalid peer mapping")
	}
	_, err := s.db.Exec(`INSERT INTO peers(peer,kind,id,name,username,is_dialog,access_hash) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(peer) DO UPDATE SET kind=excluded.kind,id=excluded.id,
 name=CASE WHEN excluded.name='' THEN peers.name ELSE excluded.name END,
 username=CASE WHEN excluded.name='' THEN peers.username ELSE excluded.username END,
 is_dialog=MAX(peers.is_dialog,excluded.is_dialog),
 access_hash=CASE WHEN excluded.access_hash=0 THEN peers.access_hash ELSE excluded.access_hash END
WHERE peers.kind<>excluded.kind OR peers.id<>excluded.id
 OR (excluded.name<>'' AND (peers.name<>excluded.name OR peers.username<>excluded.username))
 OR peers.is_dialog<excluded.is_dialog
 OR (excluded.access_hash<>0 AND peers.access_hash<>excluded.access_hash)`, key, p.Kind, p.ID, p.Name, p.Username, isDialog, p.AccessHash)
	return err
}

func (s *sqliteStore) RemoveMissingPeers(keep map[string]bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT peer FROM peers")
	if err != nil {
		return err
	}
	var remove []string
	for rows.Next() {
		var peer string
		if err := rows.Scan(&peer); err != nil {
			rows.Close()
			return err
		}
		if !keep[peer] {
			remove = append(remove, peer)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, peer := range remove {
		if _, err := tx.Exec("DELETE FROM peers WHERE peer=?", peer); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM media WHERE peer=?", peer); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *sqliteStore) PeerProperties() (map[string]PeerInfo, error) {
	rows, err := s.db.Query("SELECT peer,kind,id,name,username,access_hash FROM peers")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]PeerInfo)
	for rows.Next() {
		var key string
		var p PeerInfo
		if err := rows.Scan(&key, &p.Kind, &p.ID, &p.Name, &p.Username, &p.AccessHash); err != nil {
			return nil, err
		}
		out[key] = p
	}
	return out, rows.Err()
}

func (s *sqliteStore) SyncMessages(peer string, rows map[int]Record) error {
	if !validPeer.MatchString(peer) {
		return errors.New("invalid media peer identity")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	existing := make(map[int]bool)
	stored, err := tx.Query("SELECT message_id FROM media WHERE peer=?", peer)
	if err != nil {
		return err
	}
	for stored.Next() {
		var id int
		if err := stored.Scan(&id); err != nil {
			stored.Close()
			return err
		}
		existing[id] = true
	}
	scanErr := stored.Err()
	if err := stored.Close(); err != nil {
		return err
	}
	if scanErr != nil {
		return scanErr
	}
	stmt, err := tx.Prepare(`INSERT INTO media(peer,message_id,media_id,media_kind,file_name,mime_type,size_bytes) VALUES(?,?,?,?,?,?,?)
ON CONFLICT(peer,message_id) DO UPDATE SET media_id=excluded.media_id,media_kind=excluded.media_kind,
 file_name=excluded.file_name,mime_type=excluded.mime_type,size_bytes=excluded.size_bytes
WHERE media.media_id<>excluded.media_id OR media.media_kind<>excluded.media_kind
 OR media.file_name<>excluded.file_name OR media.mime_type<>excluded.mime_type OR media.size_bytes<>excluded.size_bytes`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for id, r := range rows {
		if r.Peer != peer || r.MessageID != id || id <= 0 {
			return errors.New("invalid media message identity")
		}
		if r.Media == nil || r.Media.File == nil {
			continue
		}
		f := r.Media.File
		if _, err := stmt.Exec(peer, id, f.ID, r.Media.Kind, f.Name, f.MIME, f.Size); err != nil {
			return err
		}
		delete(existing, id)
	}
	for id := range existing {
		if _, err := tx.Exec("DELETE FROM media WHERE peer=? AND message_id=?", peer, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *sqliteStore) RemovePeer(peer string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM media WHERE peer=?", peer); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM peers WHERE peer=?", peer); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) RecordDownload(record Record, mediaID string, result fetchResult) error {
	if result.Status != "downloaded" {
		return nil
	}
	if !validPeer.MatchString(record.Peer) || record.AccountID <= 0 || record.MessageID <= 0 || result.Peer != record.Peer || result.MessageID != record.MessageID || mediaID == "" || result.Path == "" || result.Size < 0 || result.SHA256 == "" {
		return errors.New("invalid completed download mapping")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var value []byte
	if err := tx.QueryRow("SELECT value_json FROM properties WHERE key='archive_account'").Scan(&value); err != nil {
		return fmt.Errorf("read download catalog account: %w", err)
	}
	var accountID int64
	if err := json.Unmarshal(value, &accountID); err != nil {
		return fmt.Errorf("decode download catalog account: %w", err)
	}
	if accountID != record.AccountID {
		return errors.New("download catalog belongs to a different account")
	}
	mediaKind := record.MediaType
	if record.Media != nil && record.Media.Kind != "" {
		mediaKind = record.Media.Kind
	}
	// Completed files survive remote deletion; recording them never restores media.
	_, err = tx.Exec(`INSERT INTO files(peer,message_id,media_id,media_kind,path,size_bytes,sha256,downloaded_at) VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(peer,message_id,media_id,media_kind,path) DO UPDATE SET size_bytes=excluded.size_bytes,
 sha256=excluded.sha256,downloaded_at=excluded.downloaded_at`, record.Peer, record.MessageID, mediaID, mediaKind, result.Path, result.Size, result.SHA256, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func readStateJSON(path, key string, out any) error {
	return readSQLiteJSON(path, key, out, true)
}

func readPropertyJSON(path, key string, out any) error {
	return readSQLiteJSON(path, key, out, false)
}

func openSQLiteReadOnly(path string, runtime bool) (*sqliteStore, error) {
	db, err := connectSQLite(path, true)
	if err != nil {
		return nil, err
	}
	s := &sqliteStore{db: db, table: "properties"}
	if runtime {
		s.table = "state"
	}
	version, err := checkSQLiteVersion(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if version == 0 {
		db.Close()
		return nil, os.ErrNotExist
	}
	if err := s.checkRole(version); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func readSQLiteJSON(path, key string, out any, runtime bool) error {
	s, err := openSQLiteReadOnly(path, runtime)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.ReadJSON(key, out)
}

func writeStateJSON(path, key string, value any) error {
	s, err := openStateSQLite(path)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteJSON(key, value)
}

func readArchiveMeta(dir string, out *archiveMeta) error {
	if err := readStateJSON(filepath.Join(filepath.Dir(dir), "state.sqlite"), "archive_metadata", out); err != nil {
		return err
	}
	s, err := openSQLiteReadOnly(filepath.Join(dir, "index.sqlite"), false)
	if err != nil {
		return err
	}
	defer s.Close()
	return joinArchiveProperties(s, out)
}

func joinArchiveProperties(s *sqliteStore, out *archiveMeta) error {
	var accountID int64
	err := s.ReadJSON("archive_account", &accountID)
	if os.IsNotExist(err) && out.AccountID == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	if accountID != out.AccountID {
		return errors.New("archive state and attributes belong to different accounts")
	}
	props, err := s.PeerProperties()
	if err != nil {
		return err
	}
	for key, p := range out.Peers {
		if out.Blocked[key] != "" {
			continue
		}
		q, ok := props[key]
		if !ok || p.Kind != q.Kind || p.ID != q.ID {
			return fmt.Errorf("archive peer attributes missing or inconsistent: %s", key)
		}
		p.Name, p.Username, p.AccessHash = q.Name, q.Username, q.AccessHash
		out.Peers[key] = p
	}
	return nil
}
