package main

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestVerifyArchivedChannelPersistsForbiddenAndSkipsItAfterRestart(t *testing.T) {
	archiveDir := t.TempDir()
	a, err := openArchive(archiveDir, &failure{})
	requireOK(t, err)
	defer a.Close()
	requireOK(t, a.bind(42))
	requireOK(t, a.register("channel-7", PeerInfo{Kind: "channel", ID: 7, AccessHash: 77}))
	calls := 0
	r := &receiver{archive: a, self: 42, batch: 2, api: tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, _ bin.Decoder) error {
		calls++
		if _, ok := in.(*tg.ChannelsGetParticipantsRequest); !ok {
			t.Fatalf("unexpected archived peer check RPC %T", in)
		}
		return tgerr.New(406, "CHANNEL_PRIVATE")
	}))}
	requireOK(t, r.verifyArchivedPeers(context.Background()))
	if !a.blocked("channel-7") || calls != 1 {
		t.Fatal("forbidden archived channel was not durably blocked")
	}
	requireOK(t, r.verifyArchivedPeers(context.Background()))
	if calls != 1 {
		t.Fatal("blocked archived channel was probed again")
	}
	reopened, err := openArchive(archiveDir, &failure{})
	requireOK(t, err)
	defer reopened.Close()
	if !reopened.blocked("channel-7") {
		t.Fatal("blocked archived channel was not restored after restart")
	}
}

func TestVerifyArchivedPublicChannelRemainsActiveWhenAccessible(t *testing.T) {
	a, err := openArchive(t.TempDir(), &failure{})
	requireOK(t, err)
	defer a.Close()
	requireOK(t, a.bind(42))
	requireOK(t, a.register("channel-8", PeerInfo{Kind: "channel", ID: 8, AccessHash: 88}))
	calls := 0
	r := &receiver{archive: a, self: 42, api: tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		calls++
		req, ok := in.(*tg.ChannelsGetParticipantsRequest)
		if !ok || req.Channel.(*tg.InputChannel).ChannelID != 8 || req.Limit != 1 {
			t.Fatal("archived public channel check was malformed")
		}
		out.(*tg.ChannelsChannelParticipantsBox).ChannelParticipants = &tg.ChannelsChannelParticipants{Participants: []tg.ChannelParticipantClass{}}
		return nil
	}))}
	requireOK(t, r.verifyArchivedPeers(context.Background()))
	if a.blocked("channel-8") || calls != 1 {
		t.Fatal("accessible archived channel was incorrectly blocked")
	}
}

func TestVerifyArchivedChannelKeepsActiveWhenAdminCheckIsRequired(t *testing.T) {
	a, err := openArchive(t.TempDir(), &failure{})
	requireOK(t, err)
	defer a.Close()
	requireOK(t, a.bind(42))
	requireOK(t, a.register("channel-10", PeerInfo{Kind: "channel", ID: 10, AccessHash: 100}))
	r := &receiver{archive: a, self: 42, api: tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, _ bin.Decoder) error {
		if _, ok := in.(*tg.ChannelsGetParticipantsRequest); !ok {
			t.Fatalf("unexpected archived peer check RPC %T", in)
		}
		return tgerr.New(400, "CHAT_ADMIN_REQUIRED")
	}))}
	requireOK(t, r.verifyArchivedPeers(context.Background()))
	if a.blocked("channel-10") {
		t.Fatal("admin-only verification error incorrectly blocked an active channel")
	}
}
