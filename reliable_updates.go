// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// Difference responses cross a persistence boundary before the SDK can advance
// its cursors or place OtherUpdates in an unacknowledged in-memory queue.
type reliableDifferenceMiddleware struct {
	active        func() bool
	handler       telegram.UpdateHandler
	requestRepair func(context.Context, string) error
}

var _ telegram.Middleware = (*reliableDifferenceMiddleware)(nil)

func (m *reliableDifferenceMiddleware) Handle(next tg.Invoker) telegram.InvokeFunc {
	return func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
		if err := next.Invoke(ctx, in, out); err != nil {
			return err
		}
		if m.active == nil || !m.active() {
			return nil
		}
		switch request := in.(type) {
		case *tg.UpdatesGetDifferenceRequest:
			box, ok := out.(*tg.UpdatesDifferenceBox)
			if !ok || box == nil || box.Difference == nil {
				return errors.New("invalid account difference response")
			}
			return m.accountDifference(ctx, box.Difference)
		case *tg.UpdatesGetChannelDifferenceRequest:
			box, ok := out.(*tg.UpdatesChannelDifferenceBox)
			if !ok || box == nil || box.ChannelDifference == nil {
				return errors.New("invalid channel difference response")
			}
			return m.channelDifference(ctx, request, box.ChannelDifference)
		default:
			return nil
		}
	}
}

func (m *reliableDifferenceMiddleware) accountDifference(ctx context.Context, difference tg.UpdatesDifferenceClass) error {
	switch d := difference.(type) {
	case *tg.UpdatesDifference:
		if err := m.apply(ctx, d.NewMessages, d.OtherUpdates, d.Users, d.Chats, false); err != nil {
			return err
		}
		d.NewMessages = nil
		d.OtherUpdates = remainingDifferenceUpdates(d.OtherUpdates)
		return nil
	case *tg.UpdatesDifferenceSlice:
		if err := m.apply(ctx, d.NewMessages, d.OtherUpdates, d.Users, d.Chats, false); err != nil {
			return err
		}
		d.NewMessages = nil
		d.OtherUpdates = remainingDifferenceUpdates(d.OtherUpdates)
		return nil
	case *tg.UpdatesDifferenceTooLong:
		return m.repair(ctx, "")
	case *tg.UpdatesDifferenceEmpty:
		return nil
	default:
		return errors.New("unsupported account difference response")
	}
}

func (m *reliableDifferenceMiddleware) channelDifference(ctx context.Context, request *tg.UpdatesGetChannelDifferenceRequest, difference tg.UpdatesChannelDifferenceClass) error {
	switch d := difference.(type) {
	case *tg.UpdatesChannelDifference:
		if err := m.apply(ctx, d.NewMessages, d.OtherUpdates, d.Users, d.Chats, true); err != nil {
			return err
		}
		d.NewMessages = nil
		d.OtherUpdates = remainingDifferenceUpdates(d.OtherUpdates)
		return nil
	case *tg.UpdatesChannelDifferenceTooLong:
		if request == nil || request.Channel == nil {
			return errors.New("channel difference repair has no channel")
		}
		channel, ok := request.Channel.AsNotEmpty()
		if !ok || channel.GetChannelID() <= 0 {
			return errors.New("channel difference repair has no channel identity")
		}
		if err := m.repair(ctx, fmt.Sprintf("channel-%d", channel.GetChannelID())); err != nil {
			return err
		}
		return m.apply(ctx, d.Messages, nil, d.Users, d.Chats, true)
	case *tg.UpdatesChannelDifferenceEmpty:
		return nil
	default:
		return errors.New("unsupported channel difference response")
	}
}

func remainingDifferenceUpdates(updates []tg.UpdateClass) []tg.UpdateClass {
	if len(updates) == 0 {
		return updates
	}
	remaining := make([]tg.UpdateClass, 0, len(updates))
	for _, update := range updates {
		switch update.(type) {
		case *tg.UpdateMessagePoll, *tg.UpdateUserName, *tg.UpdatePeerHistoryTTL:
			// These handled domain events have no pts/qts fields. Keeping them in
			// the SDK queue could replay stale metadata after a newer live update.
		default:
			remaining = append(remaining, update)
		}
	}
	return remaining
}

func (m *reliableDifferenceMiddleware) repair(ctx context.Context, peer string) error {
	if m.requestRepair == nil {
		return errors.New("difference recovery requires durable repair storage")
	}
	return m.requestRepair(ctx, peer)
}

func (m *reliableDifferenceMiddleware) apply(ctx context.Context, messages []tg.MessageClass, others []tg.UpdateClass, users []tg.UserClass, chats []tg.ChatClass, channel bool) error {
	if len(messages) == 0 && len(others) == 0 && len(users) == 0 && len(chats) == 0 {
		return nil
	}
	if m.handler == nil {
		return errors.New("difference recovery requires an archive handler")
	}
	updates := make([]tg.UpdateClass, 0, len(messages)+len(others))
	for _, message := range messages {
		if channel {
			updates = append(updates, &tg.UpdateNewChannelMessage{Message: message, Pts: -1, PtsCount: -1})
		} else {
			updates = append(updates, &tg.UpdateNewMessage{Message: message, Pts: -1, PtsCount: -1})
		}
	}
	// Apply edits and deletions after the message snapshot in the same response.
	updates = append(updates, others...)
	return m.handler.Handle(ctx, &tg.Updates{Updates: updates, Users: users, Chats: chats})
}
