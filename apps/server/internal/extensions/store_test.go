package extensions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func examplePackage(id string) Package {
	return Package{Manifest: Manifest{SchemaVersion: 1, ID: id, Title: "Checklist", Version: "1.0.0", APIVersion: 1, Capabilities: []string{"storage"}}, HTML: "<!doctype html><title>Checklist</title><script>document.body.dataset.ready='true'</script>"}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "extensions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func installExample(t *testing.T, s *Store, id string) Record {
	t.Helper()
	app, created, err := s.Install(context.Background(), examplePackage(id), nil)
	if err != nil || !created {
		t.Fatalf("install: %+v %v %v", app, created, err)
	}
	return app
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	if !isCode(err, code) {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestPackageCanonicalDigestAndBoundaries(t *testing.T) {
	p := examplePackage("local.checklist")
	first, digest, err := validatePackage(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Package
	if err = decodeJSON(first, &decoded); err != nil {
		t.Fatal(err)
	}
	_, same, err := validatePackage(decoded)
	if err != nil || same != digest || len(digest) != 64 {
		t.Fatal("package digest is not stable")
	}
	for _, tc := range []struct {
		name   string
		change func(*Package)
	}{
		{"unscoped", func(p *Package) { p.Manifest.ID = "checklist" }},
		{"uppercase", func(p *Package) { p.Manifest.ID = "Local.checklist" }},
		{"path", func(p *Package) { p.Manifest.ID = "local/../checklist" }},
		{"api", func(p *Package) { p.Manifest.APIVersion = 2 }},
		{"schema", func(p *Package) { p.Manifest.SchemaVersion = 2 }},
		{"capability", func(p *Package) { p.Manifest.Capabilities = []string{"exec"} }},
		{"duplicate capability", func(p *Package) { p.Manifest.Capabilities = []string{"storage", "storage"} }},
		{"html", func(p *Package) { p.HTML = strings.Repeat("a", MaxHTMLBytes+1) }},
		{"expanded package", func(p *Package) { p.HTML = strings.Repeat("<", MaxHTMLBytes) }},
		{"title", func(p *Package) { p.Manifest.Title = "bad\nname" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := examplePackage("local.checklist")
			tc.change(&candidate)
			_, _, err := validatePackage(candidate)
			requireCode(t, err, "invalid_extension_package")
		})
	}
	for _, raw := range []string{`{"manifest":{},"html":"x","unexpected":true}`, `{"html":"a","html":"b"}`, `{"html":"a","HTML":"b"}`, `{} {}`} {
		if decodeJSON([]byte(raw), new(Package)) == nil {
			t.Fatalf("accepted ambiguous or unknown fields: %s", raw)
		}
	}
}

func TestOptionalIconsPreserveExistingDigestsAndRejectUntrustedSources(t *testing.T) {
	p := examplePackage("local.checklist")
	legacy := []byte(`{"manifest":{"schemaVersion":1,"id":"local.checklist","title":"Checklist","version":"1.0.0","apiVersion":1,"capabilities":["storage"]},"html":"\u003c!doctype html\u003e\u003ctitle\u003eChecklist\u003c/title\u003e\u003cscript\u003edocument.body.dataset.ready='true'\u003c/script\u003e"}`)
	canonical, digest, err := validatePackage(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, legacy) || digest != fmt.Sprintf("%x", sha256.Sum256(legacy)) {
		t.Fatalf("omitting icon changed an existing package: %s", canonical)
	}
	for _, icon := range []Icon{IconDocument, IconChecklist} {
		p.Manifest.Icon = icon
		code, changed, err := validatePackage(p)
		if err != nil || changed == digest {
			t.Fatalf("icon not fingerprinted: %s %v", icon, err)
		}
		var decoded Package
		if err = decodeJSON(code, &decoded); err != nil || decoded.Manifest.Icon != icon {
			t.Fatalf("icon round trip: %+v %v", decoded.Manifest, err)
		}
	}
	for _, invalid := range []string{"unknown", "https://example.com/icon.svg", "data:image/svg+xml,<svg/>", "<svg/>"} {
		p.Manifest.Icon = Icon(invalid)
		_, _, err := validatePackage(p)
		requireCode(t, err, "invalid_extension_package")
	}
	for _, invalid := range []string{`"unknown"`, `"https://example.com/icon.svg"`, `"data:image/svg+xml,<svg/>"`, `"<svg/>"`, `""`, `null`, `1`, `{}`} {
		body := bytes.Replace(legacy, []byte(`"capabilities":["storage"]`), []byte(`"capabilities":["storage"],"icon":`+invalid), 1)
		if err := decodeJSON(body, new(Package)); err == nil {
			t.Fatalf("accepted unsupported icon %s", invalid)
		}
	}
}

func TestIconUpgradePreservesDataAndKeepsExistingCodeReadableOnStartup(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "extensions.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	app := installExample(t, s, "local.checklist")
	m := New(s)
	key, err := m.createSession(ctx, app.ID, SessionInput{app.Digest, app.Generation, "client", "window"})
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err = m.rpc(ctx, app.ID, key, "storage.set", json.RawMessage(`{"items":["keep"]}`), &zero); err != nil {
		t.Fatal(err)
	}
	withIcon := examplePackage(app.ID)
	withIcon.Manifest.Icon = IconChecklist
	updated, created, err := s.Install(ctx, withIcon, &app.Generation)
	if err != nil || created || updated.Icon != IconChecklist || updated.Digest == app.Digest || updated.Generation != app.Generation+1 {
		t.Fatalf("icon upgrade: %+v %v", updated, err)
	}
	_, err = m.rpc(ctx, app.ID, key, "storage.get", nil, nil)
	requireCode(t, err, "extension_session_expired")
	doc, err := s.Data(ctx, app.ID)
	if err != nil || doc.Revision != 1 || string(doc.Value) != `{"items":["keep"]}` {
		t.Fatalf("icon upgrade changed saved data: %+v %v", doc, err)
	}
	if _, err = s.Content(ctx, app.ID, app.Digest); !isCode(err, "extension_not_found") {
		t.Fatalf("old digest still executable: %v", err)
	}
	if html, err := s.Content(ctx, app.ID, updated.Digest); err != nil || html != withIcon.HTML {
		t.Fatalf("icon changed content: %q %v", html, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filename)
	if err != nil {
		t.Fatalf("reopen retained package without icon and upgraded package with icon: %v", err)
	}
	apps, err := s.List(ctx)
	if err != nil || len(apps) != 1 || apps[0].Icon != IconChecklist {
		t.Fatalf("persisted catalog icon: %+v %v", apps, err)
	}
}

func TestConcurrentWindowsCannotOverwriteTheSameRevision(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	app := installExample(t, s, "local.checklist")
	managers := []*Manager{New(s), New(s)}
	var keys [2]string
	for i, m := range managers {
		var err error
		keys[i], err = m.createSession(ctx, app.ID, SessionInput{app.Digest, app.Generation, "client", fmt.Sprintf("window-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for i, m := range managers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			zero := int64(0)
			_, err := m.rpc(ctx, app.ID, keys[i], "storage.set", json.RawMessage(fmt.Sprintf(`{"writer":%d}`, i)), &zero)
			failures <- err
		}()
	}
	close(start)
	wg.Wait()
	close(failures)
	saved, conflicts := 0, 0
	for err := range failures {
		if err == nil {
			saved++
		} else if isCode(err, "revision_conflict") {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if saved != 1 || conflicts != 1 {
		t.Fatalf("saved=%d conflicts=%d", saved, conflicts)
	}
	doc, err := s.Data(ctx, app.ID)
	if err != nil || doc.Revision != 1 {
		t.Fatalf("data after concurrent save: %+v %v", doc, err)
	}
}

func TestStorageRevisionGenerationAndDataSurviveUninstall(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	app := installExample(t, s, "local.checklist")
	m := New(s)
	key, err := m.createSession(ctx, app.ID, SessionInput{app.Digest, app.Generation, "browser", "window"})
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	doc, err := m.rpc(ctx, app.ID, key, "storage.set", json.RawMessage(`{"items":["keep me"]}`), &zero)
	if err != nil || doc.Revision != 1 {
		t.Fatalf("save: %+v %v", doc, err)
	}
	_, err = m.rpc(ctx, app.ID, key, "storage.set", json.RawMessage(`"stale"`), &zero)
	requireCode(t, err, "revision_conflict")
	_, _, err = s.Install(ctx, examplePackage(app.ID), nil)
	requireCode(t, err, "extension_conflict")
	upgraded := examplePackage(app.ID)
	upgraded.Manifest.Version = "2.0.0"
	newApp, created, err := s.Install(ctx, upgraded, &app.Generation)
	if err != nil || created || newApp.Generation != 2 || newApp.Digest == app.Digest {
		t.Fatalf("upgrade: %+v %v", newApp, err)
	}
	// The store checks scope in the document transaction, even if an in-memory
	// session was not eagerly removed by the HTTP management handler.
	_, err = m.rpc(ctx, app.ID, key, "storage.get", nil, nil)
	requireCode(t, err, "extension_session_expired")
	if _, err = s.Purge(ctx, app.ID, 1); err == nil {
		t.Fatal("purged data while installed")
	}
	app, err = s.Change(ctx, app.ID, newApp.Generation, false, false)
	if err != nil || app.Installed || app.Enabled {
		t.Fatalf("uninstall: %+v %v", app, err)
	}
	doc, err = s.Data(ctx, app.ID)
	if err != nil || doc.Revision != 1 || !strings.Contains(string(doc.Value), "keep me") {
		t.Fatalf("lost uninstall data: %+v %v", doc, err)
	}
	app, _, err = s.Install(ctx, upgraded, &app.Generation)
	if err != nil || !app.Enabled || !app.Installed {
		t.Fatalf("reinstall: %+v %v", app, err)
	}
	doc, _ = s.Data(ctx, app.ID)
	if doc.Revision != 1 {
		t.Fatal("reinstall replaced data")
	}
	app, err = s.Change(ctx, app.ID, app.Generation, false, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Purge(ctx, app.ID, 0)
	requireCode(t, err, "revision_conflict")
	doc, err = s.Purge(ctx, app.ID, 1)
	if err != nil || string(doc.Value) != "null" || doc.Revision != 2 {
		t.Fatalf("purge: %+v %v", doc, err)
	}
}

func TestSessionsAreScopedBoundedAndExpire(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	app := installExample(t, s, "local.one")
	other := installExample(t, s, "local.two")
	m := New(s)
	now := time.Now()
	m.now = func() time.Time { return now }
	input := SessionInput{app.Digest, app.Generation, "client", "window"}
	key, err := m.createSession(ctx, app.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.rpc(ctx, other.ID, key, "storage.get", nil, nil)
	requireCode(t, err, "extension_session_expired")
	key, err = m.createSession(ctx, app.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(s).rpc(ctx, app.ID, key, "storage.get", nil, nil)
	requireCode(t, err, "extension_session_expired")
	next, err := m.createSession(ctx, app.ID, input)
	if err != nil || next == key {
		t.Fatal("independent frames did not allocate separate capabilities")
	}
	_, err = m.rpc(ctx, app.ID, key, "storage.get", nil, nil)
	if err != nil {
		t.Fatalf("same client/window revoked another frame: %v", err)
	}
	_, err = m.rpc(ctx, app.ID, next, "host.exec", nil, nil)
	requireCode(t, err, "invalid_extension_rpc")
	now = now.Add(sessionTTL + time.Second)
	_, err = m.rpc(ctx, app.ID, next, "storage.get", nil, nil)
	requireCode(t, err, "extension_session_expired")
	key, err = m.createSession(ctx, app.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	app, err = s.Change(ctx, app.ID, app.Generation, false, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.rpc(ctx, app.ID, key, "storage.get", nil, nil)
	requireCode(t, err, "extension_session_expired")
	_, err = m.createSession(ctx, app.ID, SessionInput{app.Digest, app.Generation, "client", "window"})
	requireCode(t, err, "extension_session_expired")
	for i := 0; i < maxSessions; i++ {
		m.sessions[strings.Repeat("x", i+1)] = session{app: other.ID, client: "other", window: "other", expires: now.Add(time.Hour)}
	}
	_, err = m.createSession(ctx, other.ID, SessionInput{other.Digest, other.Generation, "new-client", "new-window"})
	requireCode(t, err, "extension_session_limit")
}

func TestDelayedRevocationPreservesNewInstallationSessions(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	app := installExample(t, s, "local.checklist")
	m := New(s)
	old, err := m.createSession(ctx, app.ID, SessionInput{app.Digest, app.Generation, "client", "window"})
	if err != nil {
		t.Fatal(err)
	}
	updated, _, err := s.Install(ctx, examplePackage(app.ID), &app.Generation)
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := m.createSession(ctx, app.ID, SessionInput{updated.Digest, updated.Generation, "client", "window"})
	if err != nil {
		t.Fatal(err)
	}
	m.revokeBefore(app.ID, updated.Generation)
	if _, ok := m.sessions[old]; ok {
		t.Fatal("old session survived revocation")
	}
	if _, err = m.rpc(ctx, app.ID, newKey, "storage.get", nil, nil); err != nil {
		t.Fatalf("new session was revoked: %v", err)
	}
	// An even later notification from the older install must not revoke the
	// active generation created by a subsequent management transaction.
	newest, _, err := s.Install(ctx, examplePackage(app.ID), &updated.Generation)
	if err != nil {
		t.Fatal(err)
	}
	newestKey, err := m.createSession(ctx, app.ID, SessionInput{newest.Digest, newest.Generation, "client", "window"})
	if err != nil {
		t.Fatal(err)
	}
	m.revokeBefore(app.ID, updated.Generation)
	if _, err = m.rpc(ctx, app.ID, newestKey, "storage.get", nil, nil); err != nil {
		t.Fatalf("newer session was revoked by delayed notification: %v", err)
	}
}

func TestDataLimitRejectsOversizeWithoutChangingSavedRevision(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	app := installExample(t, s, "local.checklist")
	m := New(s)
	key, err := m.createSession(ctx, app.ID, SessionInput{app.Digest, app.Generation, "client", "window"})
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	_, err = m.rpc(ctx, app.ID, key, "storage.set", json.RawMessage(`"`+strings.Repeat("a", MaxDataBytes)+`"`), &zero)
	requireCode(t, err, "invalid_extension_data")
	doc, err := s.Data(ctx, app.ID)
	if err != nil || doc.Revision != 0 || string(doc.Value) != "null" {
		t.Fatalf("oversize write changed stored data: %+v %v", doc, err)
	}
}

func TestSessionDataCannotBeRetargetedToAnotherStore(t *testing.T) {
	ctx := context.Background()
	first, second := testStore(t), testStore(t)
	app := installExample(t, first, "local.checklist")
	installExample(t, second, app.ID)
	m := New(first)
	key, err := m.createSession(ctx, app.ID, SessionInput{app.Digest, app.Generation, "client", "window"})
	if err != nil {
		t.Fatal(err)
	}
	session := m.sessions[key]
	zero := int64(0)
	_, err = second.sessionData(ctx, session, json.RawMessage(`"old store draft"`), &zero)
	requireCode(t, err, "extension_store_changed")
	doc, err := second.Data(ctx, app.ID)
	if err != nil || doc.Revision != 0 || string(doc.Value) != "null" {
		t.Fatalf("cross-store write changed data: %+v %v", doc, err)
	}
}

func TestStoreIdentityPersistsAndInvalidDatabasesAreNotReinitialized(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "extensions.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	identity := s.ID()
	installExample(t, s, "local.one")
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID() != identity {
		t.Fatal("store identity changed")
	}
	_ = s.Close()
	// A filesystem backup taken after closing the writer retains its complete
	// schema, immutable code and identity when opened from another path.
	backup := filepath.Join(t.TempDir(), "extensions.sqlite")
	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(backup, contents, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	apps, err := restored.List(context.Background())
	if err != nil || restored.ID() != identity || len(apps) != 1 || apps[0].ID != "local.one" {
		t.Fatalf("closed backup was rejected or changed: %+v %v", apps, err)
	}
	_ = restored.Close()
	baseline, err := migrationFiles.ReadFile("migrations/0001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	dataDDL := string(baseline)[strings.Index(string(baseline), "CREATE TABLE extension_data"):]
	rebuildData := func(ddl string) string {
		return "DROP TABLE extension_data; " + ddl + " INSERT INTO extension_data(app_id,value,revision) VALUES('local.one','null',0)"
	}
	for _, tc := range []struct{ name, setup string }{
		{"wrong marker", "CREATE TABLE unrelated(value TEXT); INSERT INTO unrelated VALUES ('kept'); PRAGMA application_id=123; PRAGMA user_version=1"},
		{"unversioned", "CREATE TABLE unrelated(value TEXT); INSERT INTO unrelated VALUES ('kept')"},
		{"empty", ""},
		{"missing identity", "DELETE FROM extension_identity"},
		{"unknown layout", "CREATE TABLE unrelated(value TEXT)"},
		{"extra column", "ALTER TABLE extension_data ADD COLUMN surprise TEXT"},
		{"wrong column type", rebuildData(strings.Replace(dataDDL, "revision INTEGER", "revision NUMERIC", 1))},
		{"missing primary key", rebuildData(strings.Replace(dataDDL, "app_id TEXT PRIMARY KEY", "app_id TEXT", 1))},
		{"missing not null", rebuildData(strings.Replace(dataDDL, "app_id TEXT PRIMARY KEY NOT NULL", "app_id TEXT PRIMARY KEY", 1))},
		{"missing foreign key", rebuildData(strings.Replace(dataDDL, " REFERENCES extension_apps(id)", "", 1))},
		{"missing check constraint", rebuildData(strings.Replace(dataDDL, " CHECK (typeof(revision) = 'integer' AND revision >= 0)", "", 1))},
		{"missing default", rebuildData(strings.Replace(dataDDL, " DEFAULT 'null'", "", 1))},
		{"changed code", "UPDATE extension_code SET package_json='{}'"},
		{"missing data", "DELETE FROM extension_data"},
		{"newer schema", "PRAGMA user_version=2"},
		{"text revision corruption", "PRAGMA ignore_check_constraints=ON; UPDATE extension_data SET revision='bad'"},
		{"fractional revision corruption", "PRAGMA ignore_check_constraints=ON; UPDATE extension_data SET revision=1.5"},
		{"text generation corruption", "PRAGMA ignore_check_constraints=ON; UPDATE extension_apps SET generation='bad'"},
		{"fractional generation corruption", "PRAGMA ignore_check_constraints=ON; UPDATE extension_apps SET generation=1.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "extensions.sqlite")
			if tc.name != "wrong marker" && tc.name != "unversioned" && tc.name != "empty" {
				store, err := Open(file)
				if err != nil {
					t.Fatal(err)
				}
				installExample(t, store, "local.one")
				_ = store.Close()
			}
			db, err := sql.Open("sqlite", file)
			if err != nil {
				t.Fatal(err)
			}
			if tc.setup != "" {
				if _, err = db.Exec(tc.setup); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err = db.Exec("PRAGMA user_version=0"); err != nil {
					t.Fatal(err)
				}
			}
			_ = db.Close()
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if opened, err := Open(file); err == nil {
				_ = opened.Close()
				t.Fatal("invalid store was accepted")
			}
			after, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("invalid database was modified")
			}
		})
	}
}

func TestMetadataQueriesDoNotMaterializeApplicationHTML(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	p := examplePackage("local.large")
	p.HTML = strings.Repeat("x", MaxHTMLBytes)
	app, _, err := s.Install(ctx, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	var id, digest string
	var manifest []byte
	var generation int64
	var enabled, installed bool
	if err = s.db.QueryRowContext(ctx, recordQuery+" WHERE a.id=?", app.ID).Scan(&id, &manifest, &digest, &generation, &enabled, &installed); err != nil {
		t.Fatal(err)
	}
	if len(manifest) > 1024 || strings.Contains(string(manifest), `"html"`) {
		t.Fatalf("metadata query loaded application code: %d bytes", len(manifest))
	}
	var descriptor Manifest
	if err = decodeJSON(manifest, &descriptor); err != nil || descriptor.ID != app.ID {
		t.Fatalf("metadata query lost its manifest: %+v %v", descriptor, err)
	}
	apps, err := s.List(ctx)
	if err != nil || len(apps) != 1 || apps[0].Digest != app.Digest || apps[0].Title != p.Manifest.Title {
		t.Fatalf("catalog with large HTML: %+v %v", apps, err)
	}
	m := New(s)
	key, err := m.createSession(ctx, app.ID, SessionInput{app.Digest, app.Generation, "client", "window"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.rpc(ctx, app.ID, key, "storage.get", nil, nil); err != nil {
		t.Fatal(err)
	}
	html, err := s.Content(ctx, app.ID, app.Digest)
	if err != nil || html != p.HTML {
		t.Fatalf("metadata optimization changed content: %d bytes %v", len(html), err)
	}
}

func TestNullPrimaryKeysCannotHideOrphanData(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	app := installExample(t, s, "local.one")
	for _, query := range []string{
		"INSERT INTO extension_code(digest,package_json) VALUES(NULL,'{}')",
		"INSERT INTO extension_apps(id,digest,generation,enabled,installed) VALUES(NULL,'" + app.Digest + "',1,1,1)",
		"INSERT INTO extension_data(app_id,value,revision) VALUES(NULL,'null',0)",
	} {
		if _, err := s.db.ExecContext(ctx, query); err == nil {
			t.Fatalf("baseline permitted a NULL primary key: %s", query)
		}
	}
	// Build a corrupted row under a temporarily weakened fixture schema, then
	// restore the exact owned DDL. The final file has the correct fingerprint;
	// opening it must still reject the hidden NULL orphan without repairing it.
	file := filepath.Join(t.TempDir(), "extensions.sqlite")
	owned, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	installExample(t, owned, "local.one")
	_ = owned.Close()
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	var ddl string
	if err = db.QueryRow("SELECT sql FROM sqlite_schema WHERE name='extension_data'").Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	weakened := strings.Replace(ddl, "app_id TEXT PRIMARY KEY NOT NULL", "app_id TEXT PRIMARY KEY", 1)
	if weakened == ddl {
		t.Fatal("fixture did not remove the target NOT NULL")
	}
	if _, err = db.Exec("PRAGMA writable_schema=ON"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE sqlite_schema SET sql=? WHERE name='extension_data'", weakened); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	db, err = sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO extension_data(app_id,value,revision) VALUES(NULL,'null',0); PRAGMA writable_schema=ON"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE sqlite_schema SET sql=? WHERE name='extension_data'", ddl); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(file); err == nil {
		_ = opened.Close()
		t.Fatal("NULL orphan corruption was accepted")
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("opening a corrupt store modified it")
	}
}
