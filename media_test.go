package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

func samplePoll() *tg.MessageMediaPoll {
	m := &tg.MessageMediaPoll{Poll: tg.Poll{ID: 77, Question: tg.TextWithEntities{Text: "Pick a date"}, Answers: []tg.PollAnswer{{Text: tg.TextWithEntities{Text: "Monday"}, Option: []byte{0}}, {Text: tg.TextWithEntities{Text: "Tuesday"}, Option: []byte{1}}}}}
	m.Results.SetTotalVoters(5)
	m.Results.SetResults([]tg.PollAnswerVoters{{Option: []byte{0}, Voters: 3, Chosen: true}, {Option: []byte{1}, Voters: 2}})
	return m
}

func TestMediaDocumentClassifiesAndOmitsBinaryAndAuthorization(t *testing.T) {
	m := &tg.MessageMediaDocument{Document: &tg.Document{ID: 55, AccessHash: 123456, FileReference: []byte("secret-reference"), MimeType: "audio/ogg", Size: 2048, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "note.ogg"}, &tg.DocumentAttributeAudio{Voice: true, Duration: 9, Waveform: []byte("huge-waveform")}}}}
	d := mediaDetails(m)
	if d.Kind != "voice" || d.File.Name != "note.ogg" || d.File.Duration != 9 {
		t.Fatal("document attributes not captured")
	}
	b, err := json.Marshal(d)
	requireOK(t, err)
	for _, forbidden := range []string{"secret-reference", "huge-waveform", "access_hash", "file_reference", "Waveform"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatalf("persisted excluded data: %s", forbidden)
		}
	}
	m.Document.(*tg.Document).Attributes = append(m.Document.(*tg.Document).Attributes, &tg.DocumentAttributeSticker{Alt: "sticker"})
	if mediaDetails(m).Kind != "sticker" {
		t.Fatal("sticker classification missing")
	}
}

func TestMediaCapturesSmallStructuredFields(t *testing.T) {
	contact := mediaDetails(&tg.MessageMediaContact{PhoneNumber: "+123456789", FirstName: "Ada", Vcard: "BEGIN:VCARD", UserID: 123})
	if contact.Contact.Phone != "+123456789" || contact.Contact.VCard != "BEGIN:VCARD" || contact.Contact.UserID != "123" {
		t.Fatal("contact information missing")
	}
	location := mediaDetails(&tg.MessageMediaGeoLive{Geo: &tg.GeoPoint{Lat: 31.2, Long: 121.5, AccuracyRadius: 20, AccessHash: 999}, Period: 60, Heading: 90})
	if !location.Location.Live || location.Location.Latitude != 31.2 || location.Location.Period != 60 {
		t.Fatal("live location metadata missing")
	}
	venue := mediaDetails(&tg.MessageMediaVenue{Geo: &tg.GeoPoint{Lat: 31.2, Long: 121.5}, Title: "Station", Address: "1 Road", Provider: "gplaces", VenueID: "abc"})
	if venue.Venue.Address != "1 Road" || venue.Location.Longitude != 121.5 {
		t.Fatal("venue metadata missing")
	}
	web := mediaDetails(&tg.MessageMediaWebPage{Webpage: &tg.WebPage{URL: "https://example.org", Title: "Article", Description: "Short description"}})
	if web.WebPage.Title != "Article" {
		t.Fatal("web page metadata missing")
	}
}

func TestMediaSizeLimitsAndAlbumLinks(t *testing.T) {
	d := mediaDetails(&tg.MessageMediaContact{PhoneNumber: "+123", FirstName: "Ada", Vcard: strings.Repeat("v", maxVCardBytes+1)})
	if d.Contact.Phone != "+123" || d.Contact.VCard != "" || len(d.Omitted) != 1 {
		t.Fatal("large vcard was saved or small fields lost")
	}
	d = mediaDetails(&tg.MessageMediaWebPage{Webpage: &tg.WebPage{URL: "https://example.org", Description: strings.Repeat("x", maxMediaBytes)}})
	b, err := json.Marshal(d)
	requireOK(t, err)
	if len(b) > maxMediaBytes || d.WebPage != nil || d.OmissionReason == "" {
		t.Fatal("oversize structured data was saved")
	}
	m := message(&tg.PeerChannel{ChannelID: 9}, 10, "photo")
	m.GroupedID = 123
	if messageURL(m) != "https://t.me/c/9/10?single" {
		t.Fatal("album member link invalid")
	}
	if messageURL(message(&tg.PeerUser{UserID: 7}, 1, "private")) != "" {
		t.Fatal("invented private-chat HTTPS link")
	}
}

func TestLivePollResultsPreservePrivateChoiceAndDeleteCleanly(t *testing.T) {
	a, r := fixture(t)
	m := message(&tg.PeerChannel{ChannelID: 9}, 1, "")
	m.Media = samplePoll()
	requireOK(t, r.records([]tg.MessageClass{m}, true, 0))
	fence := a.fence()
	update := &tg.UpdateMessagePoll{PollID: 77, Results: tg.PollResults{Min: true}}
	update.Results.SetTotalVoters(8)
	update.Results.SetResults([]tg.PollAnswerVoters{{Option: []byte{0}, Voters: 6}})
	requireOK(t, r.handler().Handle(context.Background(), &tg.UpdateShort{Update: update}))
	poll := a.rows["channel-9"][1].Media.Poll
	if *poll.TotalVoters != 8 || *poll.Options[0].Voters != 6 || poll.Options[0].Chosen == nil || !*poll.Options[0].Chosen {
		t.Fatal("poll update lost counts or account choice")
	}
	requireOK(t, r.records([]tg.MessageClass{m}, false, fence))
	if *a.rows["channel-9"][1].Media.Poll.TotalVoters != 8 {
		t.Fatal("stale RPC replaced live poll results")
	}
	requireOK(t, a.remove("channel-9", []int{1}, true, 0))
	requireOK(t, a.updatePoll(update))
	if fileText(t, a, "channel-9") != "" {
		t.Fatal("poll update resurrected deleted metadata")
	}
}

func TestProtectedStructuredDataNeverArchived(t *testing.T) {
	a, r := fixture(t)
	m := message(&tg.PeerUser{UserID: 7}, 1, "")
	m.Media = &tg.MessageMediaContact{PhoneNumber: "+123456789"}
	m.TTLPeriod = 60
	requireOK(t, r.records([]tg.MessageClass{m}, true, 0))
	if len(a.rows) != 0 {
		t.Fatal("structured self-destruct content archived")
	}
}
