package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func TestReliableDifferenceBlocksRPCSuccessUntilHandlerCompletes(t *testing.T) {
	entered := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan error, 1)
	m := &reliableDifferenceMiddleware{active: func() bool { return true }, handler: updateHandlerFunc(func(context.Context, tg.UpdatesClass) error {
		close(entered)
		<-finish
		return nil
	})}
	invoke := m.Handle(telegram.InvokeFunc(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
		out.(*tg.UpdatesChannelDifferenceBox).ChannelDifference = &tg.UpdatesChannelDifference{
			OtherUpdates: []tg.UpdateClass{&tg.UpdateDeleteChannelMessages{ChannelID: 7, Messages: []int{1}, Pts: 10, PtsCount: 1}},
		}
		return nil
	}))
	go func() {
		done <- invoke(context.Background(), &tg.UpdatesGetChannelDifferenceRequest{Channel: &tg.InputChannel{ChannelID: 7}}, &tg.UpdatesChannelDifferenceBox{})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("difference handler did not run")
	}
	select {
	case err := <-done:
		close(finish)
		t.Fatalf("RPC returned before the handler completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(finish)
	select {
	case err := <-done:
		requireOK(t, err)
	case <-time.After(time.Second):
		t.Fatal("RPC did not complete after persistence finished")
	}
}

func TestReliableDifferenceForwardsSnapshotsThenEditsWithEntities(t *testing.T) {
	for _, kind := range []string{"account", "account_slice", "channel", "channel_too_long"} {
		t.Run(kind, func(t *testing.T) {
			channel := kind == "channel" || kind == "channel_too_long"
			peer := tg.PeerClass(&tg.PeerUser{UserID: 7})
			if channel {
				peer = &tg.PeerChannel{ChannelID: 7}
			}
			msg := message(peer, 1, "synthetic snapshot")
			edit := tg.UpdateClass(&tg.UpdateEditMessage{Message: message(peer, 1, "synthetic edit")})
			if channel {
				edit = &tg.UpdateEditChannelMessage{Message: message(peer, 1, "synthetic edit")}
			}
			users := []tg.UserClass{&tg.User{ID: 8, FirstName: "Synthetic"}}
			chats := []tg.ChatClass{&tg.Channel{ID: 7, Title: "Synthetic"}}
			state := tg.UpdatesState{Pts: 10, Qts: 11, Date: 1700000000, Seq: 12}
			var received *tg.Updates
			var repairs []string
			middleware := &reliableDifferenceMiddleware{active: func() bool { return true }, handler: updateHandlerFunc(func(_ context.Context, updates tg.UpdatesClass) error {
				received = updates.(*tg.Updates)
				return nil
			}), requestRepair: func(_ context.Context, peer string) error {
				repairs = append(repairs, peer)
				return nil
			}}
			var input bin.Encoder = &tg.UpdatesGetDifferenceRequest{}
			var output bin.Decoder = &tg.UpdatesDifferenceBox{}
			next := telegram.InvokeFunc(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
				switch kind {
				case "account":
					out.(*tg.UpdatesDifferenceBox).Difference = &tg.UpdatesDifference{NewMessages: []tg.MessageClass{msg}, OtherUpdates: []tg.UpdateClass{edit}, Users: users, Chats: chats, State: state}
				case "account_slice":
					out.(*tg.UpdatesDifferenceBox).Difference = &tg.UpdatesDifferenceSlice{NewMessages: []tg.MessageClass{msg}, OtherUpdates: []tg.UpdateClass{edit}, Users: users, Chats: chats, IntermediateState: state}
				case "channel":
					out.(*tg.UpdatesChannelDifferenceBox).ChannelDifference = &tg.UpdatesChannelDifference{NewMessages: []tg.MessageClass{msg}, OtherUpdates: []tg.UpdateClass{edit}, Users: users, Chats: chats, Pts: 10, Timeout: 15, Final: true}
				case "channel_too_long":
					out.(*tg.UpdatesChannelDifferenceBox).ChannelDifference = &tg.UpdatesChannelDifferenceTooLong{Messages: []tg.MessageClass{msg}, Users: users, Chats: chats}
				}
				return nil
			})
			if channel {
				input = &tg.UpdatesGetChannelDifferenceRequest{Channel: &tg.InputChannel{ChannelID: 7}}
				output = &tg.UpdatesChannelDifferenceBox{}
			}
			requireOK(t, middleware.Handle(next)(context.Background(), input, output))
			if received == nil || !reflect.DeepEqual(received.Users, users) || !reflect.DeepEqual(received.Chats, chats) {
				t.Fatal("difference entities were not forwarded")
			}
			if channel {
				update, ok := received.Updates[0].(*tg.UpdateNewChannelMessage)
				if !ok || update.Message != msg || update.Pts != -1 || update.PtsCount != -1 {
					t.Fatal("channel snapshot did not use the SDK update wrapper")
				}
			} else {
				update, ok := received.Updates[0].(*tg.UpdateNewMessage)
				if !ok || update.Message != msg || update.Pts != -1 || update.PtsCount != -1 {
					t.Fatal("account snapshot did not use the SDK update wrapper")
				}
			}
			if kind != "channel_too_long" && (len(received.Updates) != 2 || received.Updates[1] != edit) {
				t.Fatal("edits were not applied after their message snapshot")
			}
			if kind == "channel_too_long" && !reflect.DeepEqual(repairs, []string{"channel-7"}) {
				t.Fatal("truncated channel recovery did not request repair")
			}
			var returnedMessages []tg.MessageClass
			var returnedOthers []tg.UpdateClass
			if channel {
				if response, ok := output.(*tg.UpdatesChannelDifferenceBox).ChannelDifference.(*tg.UpdatesChannelDifference); ok {
					returnedMessages, returnedOthers = response.NewMessages, response.OtherUpdates
					if response.Pts != 10 || response.Timeout != 15 || !response.Final {
						t.Fatal("channel protocol counters or flags changed")
					}
				}
			} else {
				switch response := output.(*tg.UpdatesDifferenceBox).Difference.(type) {
				case *tg.UpdatesDifference:
					returnedMessages, returnedOthers = response.NewMessages, response.OtherUpdates
					if response.State != state {
						t.Fatal("account protocol state changed")
					}
				case *tg.UpdatesDifferenceSlice:
					returnedMessages, returnedOthers = response.NewMessages, response.OtherUpdates
					if response.IntermediateState != state {
						t.Fatal("intermediate protocol state changed")
					}
				}
			}
			if kind != "channel_too_long" && (len(returnedMessages) != 0 || !reflect.DeepEqual(returnedOthers, []tg.UpdateClass{edit})) {
				t.Fatal("SDK response did not suppress snapshots and retain edits")
			}
		})
	}
}

func TestReliableDifferenceHandlerFailurePreventsCursorAcknowledgment(t *testing.T) {
	failed := errors.New("synthetic persistence failure")
	handled, acknowledged := 0, false
	middleware := &reliableDifferenceMiddleware{active: func() bool { return true }, handler: updateHandlerFunc(func(_ context.Context, updates tg.UpdatesClass) error {
		handled = len(updates.(*tg.Updates).Updates)
		return failed
	})}
	invoke := middleware.Handle(telegram.InvokeFunc(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
		out.(*tg.UpdatesChannelDifferenceBox).ChannelDifference = &tg.UpdatesChannelDifference{Pts: 10, OtherUpdates: []tg.UpdateClass{&tg.UpdateDeleteChannelMessages{ChannelID: 7, Messages: []int{1}, Pts: 10, PtsCount: 1}}}
		return nil
	}))
	api := tg.NewClient(invoke)
	difference, err := api.UpdatesGetChannelDifference(context.Background(), &tg.UpdatesGetChannelDifferenceRequest{Channel: &tg.InputChannel{ChannelID: 7}})
	if err == nil {
		acknowledged = true
	}
	if !errors.Is(err, failed) || difference != nil || handled != 1 || acknowledged {
		t.Fatal("failed durable callback allowed the difference to be acknowledged")
	}
}

func TestReliableDifferenceTooLongPersistsRepairBeforeReturning(t *testing.T) {
	for _, channel := range []bool{false, true} {
		t.Run(map[bool]string{false: "account", true: "channel"}[channel], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.sqlite")
			middleware := &reliableDifferenceMiddleware{active: func() bool { return true }, requestRepair: func(_ context.Context, peer string) error {
				return writeStateJSON(path, "synthetic_repair", peer)
			}}
			var input bin.Encoder = &tg.UpdatesGetDifferenceRequest{}
			var output bin.Decoder = &tg.UpdatesDifferenceBox{}
			next := telegram.InvokeFunc(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
				if channel {
					out.(*tg.UpdatesChannelDifferenceBox).ChannelDifference = &tg.UpdatesChannelDifferenceTooLong{Dialog: &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 7}, Pts: 100}}
				} else {
					out.(*tg.UpdatesDifferenceBox).Difference = &tg.UpdatesDifferenceTooLong{Pts: 100}
				}
				return nil
			})
			if channel {
				input = &tg.UpdatesGetChannelDifferenceRequest{Channel: &tg.InputChannel{ChannelID: 7}}
				output = &tg.UpdatesChannelDifferenceBox{}
			}
			requireOK(t, middleware.Handle(next)(context.Background(), input, output))
			var peer string
			requireOK(t, readStateJSON(path, "synthetic_repair", &peer))
			expected := ""
			if channel {
				expected = "channel-7"
			}
			if peer != expected {
				t.Fatal("durable repair request was not readable after RPC completion")
			}
		})
	}
}

func TestReliableDifferenceRepairFailurePreventsSuccess(t *testing.T) {
	failed := errors.New("synthetic repair storage failure")
	middleware := &reliableDifferenceMiddleware{active: func() bool { return true }, requestRepair: func(context.Context, string) error { return failed }}
	invoke := middleware.Handle(telegram.InvokeFunc(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
		out.(*tg.UpdatesDifferenceBox).Difference = &tg.UpdatesDifferenceTooLong{Pts: 100}
		return nil
	}))
	if err := invoke(context.Background(), &tg.UpdatesGetDifferenceRequest{}, &tg.UpdatesDifferenceBox{}); !errors.Is(err, failed) {
		t.Fatal("failed repair storage allowed RPC success")
	}
}

func TestReliableDifferenceSkipsEncryptedMessagesAndNonDifferenceRPCs(t *testing.T) {
	handled := 0
	active := true
	middleware := &reliableDifferenceMiddleware{active: func() bool { return active }, handler: updateHandlerFunc(func(context.Context, tg.UpdatesClass) error {
		handled++
		return nil
	})}
	invoke := middleware.Handle(telegram.InvokeFunc(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
		if box, ok := out.(*tg.UpdatesDifferenceBox); ok {
			box.Difference = &tg.UpdatesDifference{NewEncryptedMessages: []tg.EncryptedMessageClass{&tg.EncryptedMessage{Bytes: []byte("synthetic encrypted payload")}}}
		}
		return nil
	}))
	requireOK(t, invoke(context.Background(), &tg.UpdatesGetDifferenceRequest{}, &tg.UpdatesDifferenceBox{}))
	requireOK(t, invoke(context.Background(), &tg.UpdatesGetStateRequest{}, &tg.UpdatesState{}))
	active = false
	requireOK(t, invoke(context.Background(), &tg.UpdatesGetDifferenceRequest{}, &tg.UpdatesDifferenceBox{}))
	if handled != 0 {
		t.Fatal("encrypted, unrelated, or pre-account-bind response reached the archive")
	}
}

func TestReliableDifferenceSuppressesOnlyAlreadyHandledDomainEvents(t *testing.T) {
	poll := &tg.UpdateMessagePoll{PollID: 1}
	name := &tg.UpdateUserName{UserID: 7, FirstName: "Synthetic"}
	ttl := &tg.UpdatePeerHistoryTTL{Peer: &tg.PeerUser{UserID: 7}, TTLPeriod: 60}
	for _, update := range []tg.UpdateClass{poll, name, ttl} {
		if _, _, hasPts := tg.IsPtsUpdate(update); hasPts {
			t.Fatal("a suppressed metadata event carries an account pts")
		}
		if _, _, _, hasChannelPts, err := tg.IsChannelPtsUpdate(update); hasChannelPts || err != nil {
			t.Fatal("a suppressed metadata event carries a channel pts")
		}
		if _, hasQts := tg.IsQtsUpdate(update); hasQts {
			t.Fatal("a suppressed metadata event carries a qts")
		}
	}
	msg := message(&tg.PeerUser{UserID: 7}, 1, "synthetic snapshot")
	edit := &tg.UpdateEditMessage{Message: message(&tg.PeerUser{UserID: 7}, 1, "synthetic edit"), Pts: 9, PtsCount: 1}
	deletion := &tg.UpdateDeleteMessages{Messages: []int{2}, Pts: 10, PtsCount: 1}
	control := &tg.UpdateChannelTooLong{ChannelID: 7}
	unsupported := &tg.UpdateConfig{}
	allUpdates := []tg.UpdateClass{poll, edit, name, deletion, ttl, control, unsupported}
	encrypted := &tg.EncryptedMessage{Bytes: []byte("synthetic encrypted payload")}
	state := tg.UpdatesState{Pts: 10, Qts: 4, Date: 1700000000, Seq: 3}
	response := &tg.UpdatesDifference{NewMessages: []tg.MessageClass{msg}, OtherUpdates: allUpdates, NewEncryptedMessages: []tg.EncryptedMessageClass{encrypted}, State: state}
	var received []tg.UpdateClass
	middleware := &reliableDifferenceMiddleware{active: func() bool { return true }, handler: updateHandlerFunc(func(_ context.Context, updates tg.UpdatesClass) error {
		received = updates.(*tg.Updates).Updates
		return nil
	})}
	invoke := middleware.Handle(telegram.InvokeFunc(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
		out.(*tg.UpdatesDifferenceBox).Difference = response
		return nil
	}))
	requireOK(t, invoke(context.Background(), &tg.UpdatesGetDifferenceRequest{}, &tg.UpdatesDifferenceBox{}))
	if len(received) != len(allUpdates)+1 || !reflect.DeepEqual(received[1:], allUpdates) {
		t.Fatal("domain filtering occurred before the durable callback")
	}
	if len(response.NewMessages) != 0 || !reflect.DeepEqual(response.OtherUpdates, []tg.UpdateClass{edit, deletion, control, unsupported}) {
		t.Fatal("response suppressed protocol-bearing or unsupported events")
	}
	if response.State != state || len(response.NewEncryptedMessages) != 1 || response.NewEncryptedMessages[0] != encrypted {
		t.Fatal("response suppression changed counters or encrypted payload")
	}
}

func TestReliableDifferenceFailurePreservesResponseForRetry(t *testing.T) {
	failed := errors.New("synthetic persistence failure")
	msg := message(&tg.PeerUser{UserID: 7}, 1, "synthetic snapshot")
	poll := &tg.UpdateMessagePoll{PollID: 1}
	response := &tg.UpdatesDifferenceSlice{NewMessages: []tg.MessageClass{msg}, OtherUpdates: []tg.UpdateClass{poll}, IntermediateState: tg.UpdatesState{Pts: 10}}
	middleware := &reliableDifferenceMiddleware{active: func() bool { return true }, handler: updateHandlerFunc(func(context.Context, tg.UpdatesClass) error { return failed })}
	invoke := middleware.Handle(telegram.InvokeFunc(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
		out.(*tg.UpdatesDifferenceBox).Difference = response
		return nil
	}))
	if err := invoke(context.Background(), &tg.UpdatesGetDifferenceRequest{}, &tg.UpdatesDifferenceBox{}); !errors.Is(err, failed) {
		t.Fatal("persistence error was not propagated")
	}
	if len(response.NewMessages) != 1 || response.NewMessages[0] != msg || len(response.OtherUpdates) != 1 || response.OtherUpdates[0] != poll || response.IntermediateState.Pts != 10 {
		t.Fatal("failed callback stripped payload needed for retry")
	}
}
