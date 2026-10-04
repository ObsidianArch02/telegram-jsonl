package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteSchemaVersion = 1

type sqliteStore struct {
	db *sql.DB
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
	if version > sqliteSchemaVersion || version < 0 {
		return 0, fmt.Errorf("unsupported SQLite schema version: %d", version)
	}
	return version, nil
}

func openSQLite(path string) (*sqliteStore, error) {
	db, err := connectSQLite(path, false)
	if err != nil {
		return nil, err
	}
	s := &sqliteStore{db: db}
	if err := s.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *sqliteStore) initialize() error {
	if _, err := checkSQLiteVersion(s.db); err != nil {
		return err
	}
	if _, err := s.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	if _, err := s.db.Exec("PRAGMA synchronous=FULL"); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS peers (
 peer TEXT PRIMARY KEY, kind TEXT NOT NULL, id INTEGER NOT NULL,
 name TEXT NOT NULL, username TEXT NOT NULL COLLATE NOCASE, is_dialog INTEGER NOT NULL
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
CREATE TABLE IF NOT EXISTS state (
 key TEXT PRIMARY KEY, value_json TEXT NOT NULL
);
PRAGMA user_version=1;`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) Close() error { return s.db.Close() }

func (s *sqliteStore) ReadJSON(key string, out any) error {
	var value []byte
	err := s.db.QueryRow("SELECT value_json FROM state WHERE key=?", key).Scan(&value)
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
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO state(key,value_json) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json", key, string(b))
	return err
}

func (s *sqliteStore) UpsertPeer(key string, p PeerInfo, isDialog bool) error {
	if !validPeer.MatchString(key) || key != fmt.Sprintf("%s-%d", p.Kind, p.ID) {
		return errors.New("invalid peer mapping")
	}
	_, err := s.db.Exec(`INSERT INTO peers(peer,kind,id,name,username,is_dialog) VALUES(?,?,?,?,?,?)
ON CONFLICT(peer) DO UPDATE SET kind=excluded.kind,id=excluded.id,
 name=CASE WHEN excluded.name='' THEN peers.name ELSE excluded.name END,
 username=CASE WHEN excluded.name='' THEN peers.username ELSE excluded.username END,
 is_dialog=MAX(peers.is_dialog,excluded.is_dialog)`, key, p.Kind, p.ID, p.Name, p.Username, isDialog)
	return err
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
	if _, err := tx.Exec("DELETE FROM media WHERE peer=?", peer); err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO media(peer,message_id,media_id,media_kind,file_name,mime_type,size_bytes) VALUES(?,?,?,?,?,?,?)")
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
	if err := tx.QueryRow("SELECT value_json FROM state WHERE key='archive_metadata'").Scan(&value); err != nil {
		return fmt.Errorf("read download catalog account: %w", err)
	}
	var meta archiveMeta
	if err := json.Unmarshal(value, &meta); err != nil {
		return fmt.Errorf("decode download catalog account: %w", err)
	}
	if meta.AccountID != record.AccountID {
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
	db, err := connectSQLite(path, true)
	if err != nil {
		return err
	}
	defer db.Close()
	version, err := checkSQLiteVersion(db)
	if err != nil {
		return err
	}
	if version == 0 {
		return os.ErrNotExist
	}
	return (&sqliteStore{db: db}).ReadJSON(key, out)
}

func writeStateJSON(path, key string, value any) error {
	s, err := openSQLite(path)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteJSON(key, value)
}

func readArchiveMeta(dir string, out *archiveMeta) error {
	return readStateJSON(filepath.Join(dir, "index.sqlite"), "archive_metadata", out)
}
