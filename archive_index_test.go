// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/tg"
)

func TestReceiverIndexesNamesAndUpdatesWithoutCreatingSenderDialogs(t *testing.T) {
	a, r := fixture(t)
	requireOK(t, r.observe([]tg.UserClass{&tg.User{ID: 7, FirstName: "\u5f20", LastName: "\u4e09", Username: "ExampleUser", AccessHash: 123}}, []tg.ChatClass{
		&tg.Chat{ID: 8, Title: "\u8ba8\u8bba\u7fa4"},
		&tg.Channel{ID: 9, Title: "\u6280\u672f\u9891\u9053", Username: "ExampleChannel", AccessHash: 456},
	}))
	if len(a.peers()) != 0 {
		t.Fatal("entity mappings became dialogs without a received message")
	}
	var name, username string
	var isDialog int
	requireOK(t, a.index.db.QueryRow("SELECT name,username,is_dialog FROM peers WHERE peer='channel-9'").Scan(&name, &username, &isDialog))
	if name != "\u6280\u672f\u9891\u9053" || username != "ExampleChannel" || isDialog != 0 {
		t.Fatal("channel display name or username was not indexed")
	}
	requireOK(t, r.records([]tg.MessageClass{message(&tg.PeerUser{UserID: 7}, 1, "private-body")}, true, 0))
	p, _ := a.peer("user-7")
	if p.Name != "\u5f20 \u4e09" || p.Username != "ExampleUser" || p.AccessHash != 123 {
		t.Fatal("message registration lost the cached identity")
	}
	requireOK(t, r.handler().Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateUserName{UserID: 7, FirstName: "\u674e", LastName: "\u56db"}}}))
	requireOK(t, a.index.db.QueryRow("SELECT name,username,is_dialog FROM peers WHERE peer='user-7'").Scan(&name, &username, &isDialog))
	if name != "\u674e \u56db" || username != "" || isDialog != 1 {
		t.Fatal("renaming or removing the username did not update the index")
	}
	requireOK(t, r.observe(nil, []tg.ChatClass{&tg.Channel{ID: 9, Min: true}}))
	requireOK(t, a.index.db.QueryRow("SELECT name FROM peers WHERE peer='channel-9'").Scan(&name))
	if name != "\u6280\u672f\u9891\u9053" {
		t.Fatal("partial channel data erased the known display name")
	}
	requireOK(t, r.records([]tg.MessageClass{message(&tg.PeerChannel{ChannelID: 9}, 2, "channel-body")}, true, 0))
	requireOK(t, r.records([]tg.MessageClass{&tg.MessageService{ID: 3, PeerID: &tg.PeerChannel{ChannelID: 9}, Action: &tg.MessageActionChatEditTitle{Title: "\u65b0\u9891\u9053"}}}, true, 0))
	requireOK(t, a.index.db.QueryRow("SELECT name FROM peers WHERE peer='channel-9'").Scan(&name))
	if name != "\u65b0\u9891\u9053" || len(a.ids("channel-9")) != 1 {
		t.Fatal("service rename failed or service message was archived")
	}
}

func TestArchiveIndexTracksMediaEditsDeletionsAndRestart(t *testing.T) {
	a, r := fixture(t)
	m := attachmentMessage(8)
	requireOK(t, r.records([]tg.MessageClass{m}, true, 0))
	var mediaID string
	requireOK(t, a.index.db.QueryRow("SELECT media_id FROM media WHERE peer='user-7' AND message_id=1").Scan(&mediaID))
	if mediaID != "55" {
		t.Fatal("media association missing")
	}
	_, err := a.index.db.Exec("DELETE FROM media")
	requireOK(t, err)
	reopened, err := openArchive(a.dir, &failure{})
	requireOK(t, err)
	defer reopened.Close()
	requireOK(t, reopened.index.db.QueryRow("SELECT media_id FROM media WHERE peer='user-7' AND message_id=1").Scan(&mediaID))
	if mediaID != "55" {
		t.Fatal("restart did not rebuild the index from JSONL")
	}
	requireOK(t, r.records([]tg.MessageClass{message(&tg.PeerUser{UserID: 7}, 1, "replacement-text")}, true, 0))
	var count int
	requireOK(t, a.index.db.QueryRow("SELECT COUNT(*) FROM media").Scan(&count))
	if count != 0 {
		t.Fatal("media association survived conversion to a text message")
	}
	requireOK(t, r.records([]tg.MessageClass{m}, true, 0))
	requireOK(t, a.remove("user-7", []int{1}, true, 0))
	requireOK(t, a.index.db.QueryRow("SELECT COUNT(*) FROM media").Scan(&count))
	if count != 0 {
		t.Fatal("media association survived deletion")
	}
	requireOK(t, a.block("user-7", "protected content"))
	requireOK(t, r.observe([]tg.UserClass{&tg.User{ID: 7, FirstName: "excluded-name"}}, nil))
	requireOK(t, a.index.db.QueryRow("SELECT COUNT(*) FROM peers WHERE peer='user-7'").Scan(&count))
	if count != 0 {
		t.Fatal("excluded peer mapping was restored by an entity update")
	}
}

func TestArchiveDoesNotImportLegacyExportsOrModifyTheirFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "channel-789.jsonl")
	original := []byte("preserved-legacy-export\n")
	requireOK(t, os.WriteFile(path, original, 0600))
	if _, err := openArchive(dir, &failure{}); err == nil {
		t.Fatal("legacy export without SQLite metadata was silently rebound")
	}
	after, err := os.ReadFile(path)
	requireOK(t, err)
	if string(after) != string(original) {
		t.Fatal("legacy exported data changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.sqlite")); !os.IsNotExist(err) {
		t.Fatal("legacy export triggered database creation")
	}
}

func TestArchiveRestartPurgesBlockedSenderMappings(t *testing.T) {
	a, r := fixture(t)
	requireOK(t, r.observe([]tg.UserClass{&tg.User{ID: 7, FirstName: "private-name"}}, nil))
	// Simulate a crash after the exclusion is durable but before catalog removal.
	a.meta.Blocked["user-7"] = "protected content"
	requireOK(t, a.saveMeta())
	reopened, err := openArchive(a.dir, &failure{})
	requireOK(t, err)
	defer reopened.Close()
	var count int
	requireOK(t, reopened.index.db.QueryRow("SELECT COUNT(*) FROM peers WHERE peer='user-7'").Scan(&count))
	if count != 0 {
		t.Fatal("restart retained excluded sender metadata")
	}
}
