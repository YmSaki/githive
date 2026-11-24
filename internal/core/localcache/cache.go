// Package localcache manages the disposable SQLite derived cache stored at
// .git/githive-cache.db (ADR-0035). Callers must treat every error as a cache
// miss and fall back transparently to reading refs/projects/* directly.
package localcache

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

// SchemaVersion is bumped whenever the cache table layout or semantics change.
const SchemaVersion = 3

const cacheFileName = "githive-cache.db"

// RefMetaEntry represents one cached ref's meta.json snapshot.
type RefMetaEntry struct {
	RefName     string
	HeadOID     string
	Feature     string
	EntityID    string
	MetaJSON    []byte
	MetaSHA256  string
	MetaBlobOID string
}

// SigEntry represents one commit's cached cryptographic signature check.
type SigEntry struct {
	CommitOID string
	SigValid  bool
	PubKeyFP  string
	KeyType   string
}

// Cache wraps a SQLite connection to .git/githive-cache.db.
type Cache struct {
	db   *sql.DB
	path string
}

// resolveGitDir locates the .git directory for a normal repo, worktree, or bare repo.
func resolveGitDir(repoDir string) (string, error) {
	dotGit := filepath.Join(repoDir, ".git")
	info, err := os.Stat(dotGit)
	if err == nil {
		if info.IsDir() {
			return dotGit, nil
		}
		raw, err := os.ReadFile(dotGit)
		if err != nil {
			return "", err
		}
		line := strings.TrimSpace(string(raw))
		if rest, ok := strings.CutPrefix(line, "gitdir:"); ok {
			p := strings.TrimSpace(rest)
			if !filepath.IsAbs(p) {
				p = filepath.Join(repoDir, p)
			}
			return filepath.Clean(p), nil
		}
	}
	// Bare repository fallback: objects/ and HEAD exist directly in repoDir.
	if _, err := os.Stat(filepath.Join(repoDir, "objects")); err == nil {
		return repoDir, nil
	}
	return "", fmt.Errorf("localcache: could not locate git dir for %s", repoDir)
}

// Open opens (or creates) .git/githive-cache.db. If the existing file is
// corrupt or has an incompatible schema version, it is removed and recreated
// once automatically.
func Open(repoDir string) (*Cache, error) {
	gitDir, err := resolveGitDir(repoDir)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(gitDir, cacheFileName)
	c, err := openAndInit(dbPath)
	if err != nil {
		if !isRebuildableError(err) {
			return nil, err
		}
		// Corrupt or incompatible cache: remove and recreate once.
		_ = os.Remove(dbPath)
		c, err = openAndInit(dbPath)
		if err != nil {
			return nil, err
		}
	}
	return c, nil
}

func isRebuildableError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "schema version mismatch") ||
		strings.Contains(msg, "not a database") ||
		strings.Contains(msg, "malformed") ||
		strings.Contains(msg, "corrupt")
}

func sqliteFileURI(dbPath string) string {
	slashed := filepath.ToSlash(dbPath)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}

func openAndInit(dbPath string) (*Cache, error) {
	db, err := sql.Open("sqlite", sqliteFileURI(dbPath))
	if err != nil {
		return nil, err
	}
	// Single-connection pool avoids SQLITE_BUSY across goroutines in-process.
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA journal_mode = DELETE;",
		"PRAGMA synchronous = NORMAL;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, err
		}
	}

	if err := ensureSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Cache{db: db, path: dbPath}, nil
}

func ensureSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS cache_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS ref_meta_index (
			ref_name TEXT PRIMARY KEY,
			head_oid TEXT NOT NULL,
			feature TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			meta_json BLOB NOT NULL,
			meta_sha256 TEXT NOT NULL,
			meta_blob_oid TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS verified_folds (
			commit_oid TEXT NOT NULL,
			reducer_ver INTEGER NOT NULL,
			max_event_id TEXT NOT NULL,
			state_json BLOB NOT NULL,
			PRIMARY KEY (commit_oid, reducer_ver)
		);`,
		`CREATE TABLE IF NOT EXISTS verified_sigs (
			commit_oid TEXT PRIMARY KEY,
			sig_valid INTEGER NOT NULL,
			pubkey_fp TEXT NOT NULL,
			key_type TEXT NOT NULL
		);`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}

	var verStr string
	err := db.QueryRow(`SELECT value FROM cache_meta WHERE key = 'schema_version'`).Scan(&verStr)
	if err == sql.ErrNoRows {
		_, err = db.Exec(`INSERT INTO cache_meta (key, value) VALUES ('schema_version', ?)`, strconv.Itoa(SchemaVersion))
		return err
	}
	if err != nil {
		return err
	}
	if verStr != strconv.Itoa(SchemaVersion) {
		return fmt.Errorf("localcache: schema version mismatch: got %q want %d", verStr, SchemaVersion)
	}
	return nil
}

// Close closes the underlying SQLite database.
func (c *Cache) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

// GetRefMetaByPrefix returns all cached ref_meta_index rows whose ref_name
// starts with prefix.
func (c *Cache) GetRefMetaByPrefix(prefix string) (map[string]RefMetaEntry, error) {
	rows, err := c.db.Query(
		`SELECT ref_name, head_oid, feature, entity_id, meta_json, meta_sha256, meta_blob_oid FROM ref_meta_index WHERE ref_name LIKE ?`,
		prefix+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]RefMetaEntry)
	for rows.Next() {
		var e RefMetaEntry
		if err := rows.Scan(&e.RefName, &e.HeadOID, &e.Feature, &e.EntityID, &e.MetaJSON, &e.MetaSHA256, &e.MetaBlobOID); err != nil {
			return nil, err
		}
		out[e.RefName] = e
	}
	return out, rows.Err()
}

// SyncRefMeta applies upserts and deletes to ref_meta_index in one transaction.
func (c *Cache) SyncRefMeta(upserts []RefMetaEntry, deletes []string) error {
	if len(upserts) == 0 && len(deletes) == 0 {
		return nil
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if len(upserts) > 0 {
		stmt, err := tx.Prepare(`INSERT OR REPLACE INTO ref_meta_index (ref_name, head_oid, feature, entity_id, meta_json, meta_sha256, meta_blob_oid) VALUES (?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		for _, u := range upserts {
			if _, err := stmt.Exec(u.RefName, u.HeadOID, u.Feature, u.EntityID, u.MetaJSON, u.MetaSHA256, u.MetaBlobOID); err != nil {
				stmt.Close()
				return err
			}
		}
		stmt.Close()
	}
	if len(deletes) > 0 {
		stmt, err := tx.Prepare(`DELETE FROM ref_meta_index WHERE ref_name = ?`)
		if err != nil {
			return err
		}
		for _, d := range deletes {
			if _, err := stmt.Exec(d); err != nil {
				stmt.Close()
				return err
			}
		}
		stmt.Close()
	}
	return tx.Commit()
}

// GetSigVerifications loads cached signature verification results for oids.
func (c *Cache) GetSigVerifications(oids []string) (map[string]SigEntry, error) {
	out := make(map[string]SigEntry, len(oids))
	if len(oids) == 0 {
		return out, nil
	}
	stmt, err := c.db.Prepare(`SELECT sig_valid, pubkey_fp, key_type FROM verified_sigs WHERE commit_oid = ?`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	for _, oid := range oids {
		var validInt int
		var fp, kt string
		err := stmt.QueryRow(oid).Scan(&validInt, &fp, &kt)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[oid] = SigEntry{
			CommitOID: oid,
			SigValid:  validInt != 0,
			PubKeyFP:  fp,
			KeyType:   kt,
		}
	}
	return out, nil
}

// PutSigVerifications stores signature verification results in one transaction.
func (c *Cache) PutSigVerifications(entries []SigEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO verified_sigs (commit_oid, sig_valid, pubkey_fp, key_type) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range entries {
		validInt := 0
		if e.SigValid {
			validInt = 1
		}
		if _, err := stmt.Exec(e.CommitOID, validInt, e.PubKeyFP, e.KeyType); err != nil {
			return err
		}
	}
	return tx.Commit()
}
